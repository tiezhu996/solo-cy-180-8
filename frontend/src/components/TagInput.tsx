// 标签输入组件：回车/按钮添加，自动去重，非空与数量上限在前端先行拒绝。
// 创建/编辑项目表单复用；readonly 模式用于归档项目只读展示。
import { useState } from 'react'
import { PROJECT_TAG_MAX_COUNT, PROJECT_TAG_MAX_LENGTH } from '../constants'

interface TagInputProps {
  value: string[]
  onChange: (tags: string[]) => void
  maxCount?: number
  readonly?: boolean
  placeholder?: string
}

export default function TagInput({
  value,
  onChange,
  maxCount = PROJECT_TAG_MAX_COUNT,
  readonly = false,
  placeholder = '输入标签后回车，如：抗战、知青',
}: TagInputProps) {
  const [draft, setDraft] = useState('')
  const [error, setError] = useState('')

  const addTag = () => {
    const name = draft.trim()
    if (!name) {
      setError('标签不能为空')
      return
    }
    if ([...name].length > PROJECT_TAG_MAX_LENGTH) {
      setError(`单个标签不能超过 ${PROJECT_TAG_MAX_LENGTH} 个字符`)
      return
    }
    if (value.some((t) => t === name)) {
      setError(`标签「${name}」已存在，重复标签会自动合并，无需重复添加`)
      setDraft('')
      return
    }
    if (value.length >= maxCount) {
      setError(`最多只能维护 ${maxCount} 个标签`)
      return
    }
    onChange([...value, name])
    setDraft('')
    setError('')
  }

  const removeTag = (name: string) => {
    onChange(value.filter((t) => t !== name))
    setError('')
  }

  return (
    <div className="tag-input">
      <div className="tag-chip-row">
        {value.length === 0 && <span className="muted">暂无标签</span>}
        {value.map((tag) => (
          <span key={tag} className="tag-chip">
            {tag}
            {!readonly && (
              <button
                type="button"
                className="tag-chip-close"
                onClick={() => removeTag(tag)}
                aria-label={`移除标签 ${tag}`}
              >
                ×
              </button>
            )}
          </span>
        ))}
      </div>
      {!readonly && (
        <>
          <div className="inline-form">
            <input
              value={draft}
              placeholder={placeholder}
              maxLength={PROJECT_TAG_MAX_LENGTH}
              onChange={(e) => {
                setDraft(e.target.value)
                setError('')
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  addTag()
                }
              }}
            />
            <button
              type="button"
              className="btn btn-plain"
              disabled={value.length >= maxCount}
              onClick={addTag}
            >
              添加标签
            </button>
          </div>
          <div className="tag-hint">
            {error ? <span className="tag-error">{error}</span> : null}
            <span className="muted">
              {value.length}/{maxCount} 个标签，重复标签自动合并
            </span>
          </div>
        </>
      )}
    </div>
  )
}
