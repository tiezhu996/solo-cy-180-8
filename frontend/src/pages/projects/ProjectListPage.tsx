// 采访项目列表页：创建、标签/关键字/状态组合检索、状态流转、删除。
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import ConfirmDialog from '../../components/ConfirmDialog'
import DataTable from '../../components/DataTable'
import EmptyState from '../../components/EmptyState'
import ProjectForm, { type ProjectFormValues } from '../../components/ProjectForm'
import StatusBadge from '../../components/StatusBadge'
import {
  PROJECT_STATUS_ARCHIVED,
  PROJECT_STATUS_COMPLETED,
  PROJECT_STATUS_DRAFT,
  PROJECT_STATUS_IN_PROGRESS,
  PROJECT_STATUS_OPTIONS,
} from '../../constants'
import { usePagination } from '../../hooks/usePagination'
import { useProjectStore } from '../../stores/projectStore'
import { formatDateTime } from '../../utils/format'
import type { Project } from '../../api/types'

const PAGE_SIZE = 20

export default function ProjectListPage() {
  const { projects, total, loading, fetchList, create, transitionStatus, remove } = useProjectStore()
  const [statusFilter, setStatusFilter] = useState('')
  const [tagFilter, setTagFilter] = useState<string[]>([])
  const [tagDraft, setTagDraft] = useState('')
  const [keywordInput, setKeywordInput] = useState('')
  const [keyword, setKeyword] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [message, setMessage] = useState('')
  const { page, totalPages, setPage, setTotal } = usePagination(1, PAGE_SIZE)

  useEffect(() => {
    setTotal(total)
  }, [total, setTotal])

  useEffect(() => {
    fetchList({ page, page_size: PAGE_SIZE, status: statusFilter, tag: tagFilter, keyword })
  }, [fetchList, page, statusFilter, tagFilter, keyword])

  // 任一筛选条件变化都回到第 1 页。
  const resetPage = useCallback(() => setPage(1), [setPage])

  const flash = (text: string) => {
    setMessage(text)
    setTimeout(() => setMessage(''), 3000)
  }

  const handleCreate = useCallback(
    async (values: ProjectFormValues) => {
      await create(values)
      setShowCreate(false)
      flash('采访项目创建成功')
    },
    [create],
  )

  const addFilterTag = () => {
    const name = tagDraft.trim()
    if (!name || tagFilter.includes(name)) {
      setTagDraft('')
      return
    }
    setTagFilter([...tagFilter, name])
    setTagDraft('')
    resetPage()
  }

  const appendFilterTag = (name: string) => {
    if (!tagFilter.includes(name)) {
      setTagFilter([...tagFilter, name])
      resetPage()
    }
  }

  const clearFilters = () => {
    setStatusFilter('')
    setTagFilter([])
    setKeyword('')
    setKeywordInput('')
    resetPage()
  }

  const nextStatus = (status: string): string => {
    switch (status) {
      case PROJECT_STATUS_DRAFT:
        return PROJECT_STATUS_IN_PROGRESS
      case PROJECT_STATUS_IN_PROGRESS:
        return PROJECT_STATUS_COMPLETED
      case PROJECT_STATUS_COMPLETED:
        return PROJECT_STATUS_ARCHIVED
      default:
        return ''
    }
  }

  const hasFilter = statusFilter !== '' || tagFilter.length > 0 || keyword !== ''

  return (
    <div className="page">
      <div className="page-header">
        <h2>采访项目</h2>
        <button className="btn btn-primary" onClick={() => setShowCreate(true)}>
          ＋ 新建采访项目
        </button>
      </div>
      {message && <div className="toast success">{message}</div>}

      <div className="filter-bar">
        <select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value)
            resetPage()
          }}
        >
          <option value="">全部状态</option>
          {PROJECT_STATUS_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
        <div className="tag-filter">
          <div className="tag-chip-row">
            {tagFilter.map((tag) => (
              <span key={tag} className="tag-chip tag-chip-active">
                {tag}
                <button
                  type="button"
                  className="tag-chip-close"
                  aria-label={`移除筛选标签 ${tag}`}
                  onClick={() => {
                    setTagFilter(tagFilter.filter((t) => t !== tag))
                    resetPage()
                  }}
                >
                  ×
                </button>
              </span>
            ))}
          </div>
          <input
            value={tagDraft}
            placeholder="按标签筛选，回车添加（多标签为且）"
            onChange={(e) => setTagDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                addFilterTag()
              }
            }}
          />
        </div>
        <div className="keyword-search">
          <input
            value={keywordInput}
            placeholder="搜索标题或受访者姓名"
            onChange={(e) => setKeywordInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                setKeyword(keywordInput.trim())
                resetPage()
              }
            }}
          />
          <button
            className="btn btn-plain btn-small"
            onClick={() => {
              setKeyword(keywordInput.trim())
              resetPage()
            }}
          >
            搜索
          </button>
        </div>
        {hasFilter && (
          <button className="btn btn-plain btn-small" onClick={clearFilters}>
            清除条件
          </button>
        )}
        <span className="filter-count">共 {total} 个项目</span>
      </div>

      <DataTable<Project>
        loading={loading}
        rows={projects}
        rowKey={(p) => p.id}
        columns={[
          {
            key: 'title',
            title: '项目标题',
            render: (p) => (
              <Link className="link" to={`/projects/${p.id}`}>
                {p.title}
              </Link>
            ),
          },
          { key: 'interviewee', title: '受访者', render: (p) => `${p.interviewee_name}（${p.birth_year}年生）` },
          {
            key: 'tags',
            title: '标签',
            render: (p) =>
              p.tags && p.tags.length > 0 ? (
                <div className="tag-chip-row">
                  {p.tags.map((tag) => (
                    <button
                      key={tag}
                      type="button"
                      className="tag-chip tag-chip-link"
                      title={`按标签「${tag}」筛选`}
                      onClick={() => appendFilterTag(tag)}
                    >
                      {tag}
                    </button>
                  ))}
                </div>
              ) : (
                <span className="muted">-</span>
              ),
          },
          { key: 'status', title: '状态', render: (p) => <StatusBadge status={p.status} type="project" /> },
          { key: 'created_at', title: '创建时间', render: (p) => formatDateTime(p.created_at) },
          {
            key: 'actions',
            title: '操作',
            render: (p) => (
              <div className="row-actions">
                <Link className="btn btn-plain btn-small" to={`/projects/${p.id}`}>
                  详情
                </Link>
                {p.status !== PROJECT_STATUS_ARCHIVED && (
                  <button
                    className="btn btn-plain btn-small"
                    onClick={async () => {
                      await transitionStatus(p.id, nextStatus(p.status))
                      flash('项目状态已更新')
                    }}
                  >
                    流转至{PROJECT_STATUS_OPTIONS.find((o) => o.value === nextStatus(p.status))?.label}
                  </button>
                )}
                <ConfirmDialog
                  title="删除采访项目"
                  message={`确定删除项目「${p.title}」吗？其问题、录音与时间轴节点将一并删除。`}
                  confirmText="删除"
                  danger
                  onConfirm={async () => {
                    await remove(p.id)
                    flash('项目已删除')
                  }}
                >
                  <button className="btn btn-danger btn-small">删除</button>
                </ConfirmDialog>
              </div>
            ),
          },
        ]}
        emptyText={hasFilter ? '没有符合筛选条件的项目' : '暂无采访项目'}
      />
      {projects.length === 0 && !loading && !hasFilter && (
        <EmptyState
          title="还没有采访项目"
          description="创建一个口述历史采访项目，开始记录受访者的故事"
          action={
            <button className="btn btn-primary" onClick={() => setShowCreate(true)}>
              新建采访项目
            </button>
          }
        />
      )}

      {total > PAGE_SIZE && (
        <div className="pagination">
          <button className="btn btn-plain btn-small" disabled={page <= 1} onClick={() => setPage(page - 1)}>
            上一页
          </button>
          <span className="pagination-info">
            第 {page} / {totalPages} 页
          </span>
          <button
            className="btn btn-plain btn-small"
            disabled={page >= totalPages}
            onClick={() => setPage(page + 1)}
          >
            下一页
          </button>
        </div>
      )}

      {showCreate && (
        <div className="modal-mask" onClick={() => setShowCreate(false)}>
          <div className="modal modal-lg" onClick={(e) => e.stopPropagation()}>
            <div className="modal-title">新建采访项目</div>
            <ProjectForm onSubmit={handleCreate} submitText="创建项目" />
          </div>
        </div>
      )}
    </div>
  )
}
