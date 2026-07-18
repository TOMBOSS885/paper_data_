# 个人论文知识库设计与实施文档

本目录是 React + Go + MySQL 个人论文知识库的第一版工程规范。目标是：单管理员、邮箱验证码认证、论文多格式导入、标准/自定义标签分类、在线阅读与下载、快速本地搜索及可选外部论文搜索，并支持 Docker Compose 部署。

## 文档导航

1. [总体架构与部署](./01-architecture-deployment.md)
2. [REST API 接口规范](./02-api-spec.md)
3. [数据模型与搜索设计](./03-data-search.md)
4. [React 前端界面与交互规范](./04-frontend-ui.md)
5. [安全基线与风险规避](./05-security.md)
6. [分步实施、测试与验收](./06-implementation-plan.md)
7. [OpenAPI 3.1 契约](./openapi.yaml)
8. [环境变量模板](./env.example)

## 结论摘要

- 后端：Go 1.24+、Gin、GORM、MySQL 8.0+；Redis 作为验证码、限流和会话撤销共享存储。
- 前端：React 19 + Vite + TypeScript；推荐 Tailwind CSS + shadcn/ui/Radix、TanStack Query/Table/Virtual、React Hook Form + Zod、PDF.js、Uppy、lucide-react。
- 会话：优先 HttpOnly、Secure、SameSite Cookie；CSRF 双提交或服务端 token；不把 JWT 放在 localStorage/sessionStorage。
- 初始化：首次打开 `/setup` 才能创建唯一管理员；创建成功后原子关闭初始化入口，后续只能通过受保护的恢复流程操作。
- 存储：论文原文使用 UUID object key 存在 Web 根目录外或私有卷，数据库只保存元数据和 object key；下载须鉴权并生成短期签名地址。
- 搜索：MySQL FULLTEXT + ngram 预留中文能力；所有筛选参数白名单化、参数化查询、分页上限和查询超时；外部检索由 Go 代理 OpenAlex/Crossref/Semantic Scholar。
- 部署：Nginx/Caddy 只暴露 443，API、MySQL、Redis 在私有 Docker 网络；API 非 root、只读根文件系统、仅 uploads/tmp 可写。

## 参考资料

- React/Vite：https://react.dev/ 、https://vite.dev/
- Ant Design/Pro：https://ant.design/ 、https://pro.ant.design/
- MUI：https://mui.com/material-ui/getting-started/
- Mantine：https://mantine.dev/
- shadcn/ui、Radix：https://ui.shadcn.com/ 、https://www.radix-ui.com/
- TanStack：https://tanstack.com/
- PDF.js：https://mozilla.github.io/pdf.js/
- Uppy：https://uppy.io/
- OpenAlex：https://docs.openalex.org/api-entities/works
- Crossref REST：https://api.crossref.org/swagger-ui/index.html
- WCAG 2.2、ARIA APG：https://www.w3.org/TR/WCAG22/ 、https://www.w3.org/WAI/ARIA/apg/
- OWASP Top 10、ASVS、Cheat Sheet Series：https://owasp.org/Top10/ 、https://owasp.org/www-project-application-security-verification-standard/ 、https://cheatsheetseries.owasp.org/
- NIST SP 800-63B：https://pages.nist.gov/800-63-4/sp800-63b.html
- CIS Docker Benchmark：https://www.cisecurity.org/benchmark/docker
