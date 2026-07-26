import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Edit2, Plus, Trash2, X } from 'lucide-react'
import {
  citationFormats,
  createCitationFormat,
  updateCitationFormat,
  deleteCitationFormat,
  citePaper,
  CitationFormat,
  ApiError
} from '../lib/api'
import { Shell } from '../App' // Assuming Shell is exported, if not I will just use standard layout.

const errorMessage = (e: unknown) => (e instanceof ApiError ? e.message : '发生未知错误')

export function CitationSettingsPage() {
  const queryClient = useQueryClient()
  const { data: formats = [], isLoading, error } = useQuery({
    queryKey: ['citationFormats'],
    queryFn: () => citationFormats().then(res => res.items)
  })

  const [editingId, setEditingId] = useState<string | number | null>(null)
  const [editName, setEditName] = useState('')
  const [editTemplate, setEditTemplate] = useState('')
  const [formError, setFormError] = useState('')

  const startEdit = (f: CitationFormat | null) => {
    setFormError('')
    if (!f) {
      setEditingId('new')
      setEditName('')
      setEditTemplate('')
    } else {
      setEditingId(f.id)
      setEditName(f.name)
      setEditTemplate(f.template)
    }
  }

  const save = async () => {
    if (!editName.trim() || !editTemplate.trim()) {
      setFormError('名称和模板不能为空')
      return
    }
    setFormError('')
    try {
      if (editingId === 'new') {
        await createCitationFormat({ name: editName, template: editTemplate })
      } else if (editingId) {
        await updateCitationFormat(editingId, { name: editName, template: editTemplate })
      }
      queryClient.invalidateQueries({ queryKey: ['citationFormats'] })
      setEditingId(null)
    } catch (e) {
      setFormError(errorMessage(e))
    }
  }

  const remove = async (id: string | number) => {
    if (!confirm('确定删除该引用格式？')) return
    try {
      await deleteCitationFormat(id)
      queryClient.invalidateQueries({ queryKey: ['citationFormats'] })
    } catch (e) {
      alert(errorMessage(e))
    }
  }

  return (
    <div style={{ padding: '24px' }}>
      <div className="page-head" style={{ marginBottom: 24 }}>
        <div>
          <p className="eyebrow">设置</p>
          <h1>引用格式管理</h1>
          <p className="muted">管理论文引用时可用的格式模板。系统内置格式不可修改或删除，但您可以复制后修改。</p>
        </div>
      </div>
      
      {error && <div className="alert error">{errorMessage(error)}</div>}
      
      <div style={{ marginBottom: 16 }}>
        <button className="button primary" onClick={() => startEdit(null)}><Plus size={16}/> 新建格式</button>
      </div>

      {editingId !== null && (
        <div className="modal-backdrop">
          <div className="modal-content" style={{ maxWidth: 600 }}>
            <h3>{editingId === 'new' ? '新建格式' : '编辑格式'}</h3>
            <div className="form-group">
              <label>格式名称</label>
              <input type="text" className="input" value={editName} onChange={e => setEditName(e.target.value)} placeholder="如：APA (Modified)" />
            </div>
            <div className="form-group">
              <label>模板语法</label>
              <textarea className="input" style={{ fontFamily: 'monospace', height: 120 }} value={editTemplate} onChange={e => setEditTemplate(e.target.value)} placeholder="{authors} ({year}). {title}. {journal}." />
              <small className="muted" style={{ display: 'block', marginTop: 8 }}>
                可用变量：{'{authors}, {authorCount}, {title}, {journal}, {year}, {doi}, {firstAuthor}, {firstAuthorLast}'}。
              </small>
            </div>
            {formError && <div className="alert error">{formError}</div>}
            <div className="modal-actions">
              <button className="button primary" onClick={save}>保存</button>
              <button className="button secondary" onClick={() => setEditingId(null)}>取消</button>
            </div>
          </div>
        </div>
      )}

      {isLoading ? <p>加载中...</p> : (
        <table className="table" style={{ width: '100%', textAlign: 'left', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid #eee' }}>
              <th style={{ padding: '12px 8px' }}>名称</th>
              <th style={{ padding: '12px 8px' }}>类型</th>
              <th style={{ padding: '12px 8px' }}>模板预览</th>
              <th style={{ padding: '12px 8px' }}>操作</th>
            </tr>
          </thead>
          <tbody>
            {formats.map(f => (
              <tr key={f.id} style={{ borderBottom: '1px solid #eee' }}>
                <td style={{ padding: '12px 8px', fontWeight: 500 }}>{f.name}</td>
                <td style={{ padding: '12px 8px' }}>{f.builtin ? <span className="badge" style={{ background: '#f1f5f9', color: '#475569', fontSize: 12, padding: '2px 6px', borderRadius: 4 }}>内置</span> : <span className="badge" style={{ background: '#ecfdf5', color: '#059669', fontSize: 12, padding: '2px 6px', borderRadius: 4 }}>自定义</span>}</td>
                <td style={{ padding: '12px 8px', fontFamily: 'monospace', fontSize: 13, color: '#666', maxWidth: 300, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{f.template}</td>
                <td style={{ padding: '12px 8px' }}>
                  <div style={{ display: 'flex', gap: 8 }}>
                    {!f.builtin && <button className="button-icon" title="编辑" onClick={() => startEdit(f)}><Edit2 size={16}/></button>}
                    {!f.builtin && <button className="button-icon" title="删除" onClick={() => remove(f.id)}><Trash2 size={16} color="#ef4444"/></button>}
                    {f.builtin && <button className="button secondary" style={{ padding: '4px 8px', fontSize: 12 }} onClick={() => {
                      setEditingId('new')
                      setEditName(f.name + ' (Copy)')
                      setEditTemplate(f.template)
                    }}>复制并修改</button>}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

export function CitationModal({ paperId, onClose }: { paperId: string; onClose: () => void }) {
  const { data: formats = [] } = useQuery({
    queryKey: ['citationFormats'],
    queryFn: () => citationFormats().then(res => res.items)
  })

  const [selectedFormat, setSelectedFormat] = useState<string>('')
  const [citationText, setCitationText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)

  // Auto-select first format
  React.useEffect(() => {
    if (formats.length > 0 && !selectedFormat) {
      handleSelect(formats[0].name)
    }
  }, [formats])

  const handleSelect = async (formatName: string) => {
    setSelectedFormat(formatName)
    setLoading(true)
    setError('')
    setCopied(false)
    try {
      const res = await citePaper(paperId, formatName)
      setCitationText(res.citation)
    } catch (e) {
      setError(errorMessage(e))
      setCitationText('')
    } finally {
      setLoading(false)
    }
  }

  const handleCopy = () => {
    if (citationText) {
      navigator.clipboard.writeText(citationText)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <div className="modal-backdrop">
      <div className="modal-content" style={{ maxWidth: 500, width: '100%' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <h3 style={{ margin: 0 }}>导出引用</h3>
          <button className="button-icon" onClick={onClose}><X size={20}/></button>
        </div>
        
        <div style={{ marginBottom: 16 }}>
          <label style={{ display: 'block', marginBottom: 8, fontWeight: 500 }}>选择格式</label>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {formats.map(f => (
              <button 
                key={f.id} 
                className={`button ${selectedFormat === f.name ? 'primary' : 'secondary'}`}
                onClick={() => handleSelect(f.name)}
              >
                {f.name}
              </button>
            ))}
          </div>
        </div>

        <div style={{ background: '#f8fafc', padding: 16, borderRadius: 8, minHeight: 100, position: 'relative', border: '1px solid #e2e8f0' }}>
          {loading ? (
            <div style={{ color: '#64748b', textAlign: 'center', marginTop: 24 }}>生成中...</div>
          ) : error ? (
            <div className="danger-text">{error}</div>
          ) : (
            <div style={{ fontFamily: 'serif', lineHeight: 1.6, whiteSpace: 'pre-wrap' }}>{citationText}</div>
          )}
        </div>

        <div className="modal-actions" style={{ marginTop: 20 }}>
          <button className="button primary" disabled={!citationText || loading} onClick={handleCopy}>
            {copied ? <Check size={16} /> : <Copy size={16} />}
            {copied ? '已复制' : '复制到剪贴板'}
          </button>
          <button className="button secondary" onClick={onClose}>关闭</button>
        </div>
      </div>
    </div>
  )
}
