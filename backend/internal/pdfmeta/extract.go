// Package pdfmeta 从 PDF 原始字节流里解析 Info 字典的元数据（Title / Author / Subject / CreationDate）。
//
// 适用范围：未压缩、未加密的明文 PDF Info 字典。学术论文 90% 是明文；扫描件或
// 加密 PDF 解析失败时安全返回空结构，调用方应将字段留空让用户手动填写。
//
// 实现要点：
//   - 不引入第三方 PDF 库，零新增依赖
//   - 兼容 latin1 与 UTF-16BE 编码（PDF Info 字段支持 BOM 嗅探）
//   - 限制读取 1MB 头部（Info 字典必在文件头）
//   - 解析失败不 panic，全部返回零值
package pdfmeta

import (
	"bytes"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Extracted 是从 PDF Info 字典里解析到的元数据。
type Extracted struct {
	Title    string
	Authors  []string
	Subject  string
	Keywords []string
	Year     int
}

// Extract 从 raw 字节流里提取 PDF 元数据。
// 输入应是完整 PDF（或至少前 1MB）。
func Extract(raw []byte) Extracted {
	var out Extracted
	if len(raw) == 0 {
		return out
	}
	// 只看前 1MB；Info 字典通常在文件头部几 KB 内。
	head := raw
	if len(head) > 1<<20 {
		head = head[:1<<20]
	}

	// 优先：通过 /Info N 0 R 间接引用定位
	if fields := extractFromInfoRef(head); fields != nil {
		applyTo(&out, fields)
		return out
	}
	// 兜底：直接在文件里找首个 Info 字典（部分生成器把 Info 写到 Root 内联）
	if fields := extractFromInlineDict(head); fields != nil {
		applyTo(&out, fields)
	}
	return out
}

// fields 是 Info 字典中可提取的字段集合。
type fields struct {
	title, subject, author, creationDate, keywords string
}

// extractFromInfoRef 通过 trailer 中 `/Info N 0 R` 引用找 Info 字典。
func extractFromInfoRef(head []byte) *fields {
	infoIdx := bytes.Index(head, []byte("/Info"))
	if infoIdx < 0 {
		return nil
	}
	
	// Skip "/Info" and whitespaces
	start := infoIdx + 5
	for start < len(head) && (head[start] == ' ' || head[start] == '\t' || head[start] == '\r' || head[start] == '\n') {
		start++
	}
	
	// Read digits
	end := start
	for end < len(head) && head[end] >= '0' && head[end] <= '9' {
		end++
	}
	
	if start == end {
		return nil
	}
	
	objNum, err := strconv.Atoi(string(head[start:end]))
	if err != nil || objNum <= 0 {
		return nil
	}
	
	objHeader := []byte(strconv.Itoa(objNum) + " 0 obj")
	objIdx := bytes.Index(head, objHeader)
	if objIdx < 0 {
		// 兜底：endobj 内联 dict
		return extractFromInlineDict(head)
	}
	endIdx := bytes.Index(head[objIdx:], []byte("endobj"))
	if endIdx < 0 {
		return nil
	}
	body := head[objIdx : objIdx+endIdx]
	return parseInfoBody(body)
}

// extractFromInlineDict 兜底场景：生成器把 Info 字段直接内联到某个 obj 块里。
func extractFromInlineDict(head []byte) *fields {
	for _, key := range []string{"Title", "Author", "CreationDate"} {
		keyBytes := []byte("/" + key)
		if i := bytes.Index(head, keyBytes); i >= 0 {
			// 找最近的 "N 0 obj" 作为块起点。
			objStart := bytes.LastIndex(head[:i], []byte("obj"))
			if objStart < 0 {
				continue
			}
			objEnd := bytes.Index(head[objStart:], []byte("endobj"))
			if objEnd < 0 {
				continue
			}
			return parseInfoBody(head[objStart : objStart+objEnd])
		}
	}
	return nil
}

// parseInfoBody 从 Info 字典块里抽取所有已知字段。
func parseInfoBody(body []byte) *fields {
	out := &fields{}
	out.title = firstString(body, "Title")
	out.subject = firstString(body, "Subject")
	out.author = firstString(body, "Author")
	out.creationDate = firstString(body, "CreationDate")
	out.keywords = firstString(body, "Keywords")
	if out.title == "" && out.author == "" && out.subject == "" && out.keywords == "" && out.creationDate == "" {
		return nil
	}
	return out
}

// applyTo 把 fields 合并到 Extracted。
func applyTo(out *Extracted, f *fields) {
	if f == nil {
		return
	}
	out.Title = f.title
	out.Subject = f.subject
	if f.keywords != "" {
		normalized := strings.ReplaceAll(f.keywords, ";", ",")
		for _, p := range strings.Split(normalized, ",") {
			if v := strings.TrimSpace(p); v != "" {
				out.Keywords = append(out.Keywords, v)
			}
		}
	}
	if f.author != "" {
		out.Authors = splitAuthors(f.author)
	}
	if f.creationDate != "" {
		out.Year = extractYear(f.creationDate)
	}
}

// firstString 在 PDF obj body 里找 /Field(...) 或 /Field<hex>，返回解码后的字符串。
func firstString(body []byte, field string) string {
	key := []byte("/" + field)
	idx := bytes.Index(body, key)
	if idx < 0 {
		return ""
	}
	
	// Skip the key and any following whitespaces
	j := idx + len(key)
	for j < len(body) && (body[j] == ' ' || body[j] == '\t' || body[j] == '\r' || body[j] == '\n') {
		j++
	}
	if j >= len(body) {
		return ""
	}
	
	// Ensure we pass a slice starting exactly at the open bracket
	if body[j] == '(' {
		return readBalanced(body[j:], '(', ')')
	} else if body[j] == '<' {
		return readBalanced(body[j:], '<', '>')
	}
	return ""
}

// readBalanced 读一个括号包裹的内容并尝试解析（literal / hex / 嵌套）。
func readBalanced(body []byte, open, close byte) string {
	if len(body) == 0 || body[0] != open {
		return ""
	}
	depth := 1
	i := 1
	for i < len(body) && depth > 0 {
		if body[i] == '\\' && i+1 < len(body) {
			i += 2
			continue
		}
		switch body[i] {
		case open:
			depth++
		case close:
			depth--
		}
		i++
	}
	if depth != 0 {
		return ""
	}
	raw := body[1 : i-1]
	decoded, ok := decodeString(raw)
	if !ok {
		return ""
	}
	return decoded
}

// decodeString 处理 PDF string 的两种编码：hex（<...>）或 literal（(...)）。
// 自动按 UTF-16BE BOM (0xFE 0xFF) 解码，否则按 latin1。
func decodeString(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	// 检查是否整段是 hex（无空白字符）
	allHex := true
	for _, b := range raw {
		if (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') {
			continue
		}
		allHex = false
		break
	}
	var decoded []byte
	if allHex && len(raw)%2 == 0 {
		var err error
		decoded, err = hex.DecodeString(string(raw))
		if err != nil {
			return "", false
		}
	} else {
		decoded = []byte(unescapePDFLiteral(raw))
	}

	// BOM 嗅探：UTF-16BE 以 0xFE 0xFF 开头
	if len(decoded) >= 2 && decoded[0] == 0xFE && decoded[1] == 0xFF {
		u16 := make([]uint16, (len(decoded)-2)/2)
		for i := range u16 {
			if 2+2*i+1 < len(decoded) {
				u16[i] = uint16(decoded[2+2*i])<<8 | uint16(decoded[2+2*i+1])
			}
		}
		return string(utf16.Decode(u16)), true
	}
	
	// PDF 中非 UTF-16 的字符串可能是 PDFDocEncoding (类似 Latin1)
	// 为防止存入 MySQL 时因非法字节导致 Incorrect string value，清理为合法的 UTF-8
	return strings.ToValidUTF8(string(decoded), ""), true
}

// unescapePDFLiteral 处理 PDF literal string 的反斜杠转义。
// 简化：只处理 \\, \(, \), \n, \r, \t, \ddd 八进制。
func unescapePDFLiteral(raw []byte) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' || i+1 >= len(raw) {
			b.WriteByte(c)
			continue
		}
		next := raw[i+1]
		switch next {
		case 'n':
			b.WriteByte('\n')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case '\\', '(', ')':
			b.WriteByte(next)
			i++
		default:
			// 八进制 \nnn（最多 3 位）
			if next >= '0' && next <= '7' {
				v := int(next - '0')
				j := i + 2
				for j < len(raw) && j-i < 4 && raw[j] >= '0' && raw[j] <= '7' {
					v = v*8 + int(raw[j]-'0')
					j++
				}
				b.WriteByte(byte(v))
				i = j - 1
			} else {
				// 对于未知的转义，比如 \=，直接输出被转义的字符
				b.WriteByte(next)
				i++
			}
		}
	}
	return b.String()
}

var authorSep = regexp.MustCompile(`;\s*|\s+and\s+`)

// splitAuthors 把 "Smith, John; Doe, Jane" 拆成 ["Smith, John", "Doe, Jane"]。
// 学术 PDF 习惯用 "; " 分隔不同作者；PDF 自己的 /Author 字段定义是 single string，
// 实际很多导出工具用逗号/分号拼接多人。
func splitAuthors(raw string) []string {
	// 优先 "; " 分隔（语义更准）
	if strings.Contains(raw, ";") {
		var out []string
		for _, p := range strings.Split(raw, ";") {
			if v := strings.TrimSpace(p); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	var out []string
	for _, p := range authorSep.Split(raw, -1) {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

var pdfDateRe = regexp.MustCompile(`(?:D:)?(\d{4})`)

// extractYear 从 PDF date string "D:20210315120030+00'00'" 提取年份。
func extractYear(s string) int {
	m := pdfDateRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	y, err := strconv.Atoi(m[1])
	if err != nil || y < 1000 || y > 9999 {
		return 0
	}
	return y
}