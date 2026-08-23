/**
 * FiltersPage — client-side email filter rules.
 *
 * Filters are stored in localStorage (no backend persistence).
 * Each filter matches incoming email and assigns a folder or label.
 *
 * Rules are evaluated client-side when listing email (InboxPage applies them
 * after fetch via applyFilterRules from lib/filters.ts). Actions are
 * display-only; the backend state is not changed. The 'move' action is a
 * no-op until backend write support is added (see lib/filters.ts).
 */

import { useState } from 'react'
import {
  type FilterRule,
  loadFilterRules,
  saveFilterRules,
} from '../lib/filters'

const BLANK_RULE: Omit<FilterRule, 'id' | 'createdAt'> = {
  name: '',
  from: '',
  to: '',
  subject: '',
  hasAttachment: false,
  action: 'label',
  actionValue: '',
  enabled: true,
}

export default function FiltersPage() {
  const [rules, setRules] = useState<FilterRule[]>(loadFilterRules)
  const [editing, setEditing] = useState<FilterRule | null>(null)
  const [showForm, setShowForm] = useState(false)

  const persist = (next: FilterRule[]) => {
    setRules(next)
    saveFilterRules(next)
  }

  const handleNew = () => {
    setEditing({
      ...BLANK_RULE,
      id: crypto.randomUUID(),
      createdAt: Date.now(),
    })
    setShowForm(true)
  }

  const handleEdit = (rule: FilterRule) => {
    setEditing({ ...rule })
    setShowForm(true)
  }

  const handleSave = () => {
    if (!editing) return
    if (!editing.name.trim()) {
      alert('Filter name is required')
      return
    }
    if (!editing.from && !editing.to && !editing.subject && !editing.hasAttachment) {
      alert('At least one match criterion is required')
      return
    }
    if ((editing.action === 'label' || editing.action === 'move') && !editing.actionValue.trim()) {
      alert('Action value is required for label and move actions')
      return
    }

    const existing = rules.find(r => r.id === editing.id)
    let next: FilterRule[]
    if (existing) {
      next = rules.map(r => (r.id === editing.id ? editing : r))
    } else {
      next = [...rules, editing]
    }
    persist(next)
    setShowForm(false)
    setEditing(null)
  }

  const handleDelete = (id: string) => {
    if (!confirm('Delete this filter?')) return
    persist(rules.filter(r => r.id !== id))
  }

  const handleToggle = (id: string) => {
    persist(rules.map(r => (r.id === id ? { ...r, enabled: !r.enabled } : r)))
  }

  const handleCancel = () => {
    setShowForm(false)
    setEditing(null)
  }

  const update = (patch: Partial<FilterRule>) => {
    if (!editing) return
    setEditing({ ...editing, ...patch })
  }

  const actionLabel = (rule: FilterRule): string => {
    switch (rule.action) {
      case 'label': return `Label "${rule.actionValue}"`
      case 'move': return `Move to "${rule.actionValue}"`
      case 'star': return 'Star'
      case 'markRead': return 'Mark read'
      default: return rule.action
    }
  }

  const criteriaLabel = (rule: FilterRule): string => {
    const parts: string[] = []
    if (rule.from) parts.push(`from: ${rule.from}`)
    if (rule.to) parts.push(`to: ${rule.to}`)
    if (rule.subject) parts.push(`subject: ${rule.subject}`)
    if (rule.hasAttachment) parts.push('has attachment')
    return parts.join(', ') || '(any)'
  }

  return (
    <div className="flex flex-col min-h-0 overflow-y-auto p-4 md:p-6">
      <div className="flex items-center justify-between mb-4 flex-wrap gap-2">
        <h1 className="text-2xl font-bold">Filters &amp; Rules</h1>
        <button
          onClick={handleNew}
          className="min-h-[44px] px-4 py-2 bg-cloistr-primary text-white rounded-lg hover:bg-cloistr-primary-hover transition text-sm"
        >
          + New filter
        </button>
      </div>

      <p className="text-sm text-cloistr-text-muted mb-4">
        Filters run client-side when you view your inbox. They apply labels, move email to folders, or perform other actions automatically.
      </p>

      {/* Filter list */}
      {rules.length === 0 ? (
        <div className="text-center py-12 text-cloistr-text-muted">
          <div className="text-4xl mb-2">🔍</div>
          <p>No filters yet</p>
          <p className="text-sm mt-1">Create one to automatically sort your email</p>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {rules.map(rule => (
            <div
              key={rule.id}
              className={`bg-cloistr-bg border border-cloistr-border rounded-lg p-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-4 ${
                !rule.enabled ? 'opacity-50' : ''
              }`}
            >
              <div className="flex-1 min-w-0">
                <div className="font-medium truncate">{rule.name}</div>
                <div className="text-xs text-cloistr-text-muted mt-0.5 truncate">
                  When: {criteriaLabel(rule)} &rarr; {actionLabel(rule)}
                </div>
              </div>
              <div className="flex items-center gap-2 flex-shrink-0">
                {/* Enable/disable toggle */}
                <button
                  onClick={() => handleToggle(rule.id)}
                  className="min-h-[44px] px-3 py-2 text-xs border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition"
                  aria-label={rule.enabled ? 'Disable filter' : 'Enable filter'}
                >
                  {rule.enabled ? 'Enabled' : 'Disabled'}
                </button>
                <button
                  onClick={() => handleEdit(rule)}
                  className="min-h-[44px] px-3 py-2 text-xs border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition"
                >
                  Edit
                </button>
                <button
                  onClick={() => handleDelete(rule.id)}
                  className="min-h-[44px] px-3 py-2 text-xs text-cloistr-error border border-cloistr-error/30 rounded-lg hover:bg-cloistr-error/5 transition"
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Filter edit form — shown as inline panel below the list */}
      {showForm && editing && (
        <div className="fixed inset-0 bg-black/50 z-50 flex items-end sm:items-center justify-center p-4">
          <div className="bg-cloistr-bg rounded-xl shadow-xl w-full max-w-lg max-h-[90dvh] overflow-y-auto">
            <div className="p-4 border-b flex items-center justify-between">
              <h2 className="text-lg font-bold">
                {rules.find(r => r.id === editing.id) ? 'Edit filter' : 'New filter'}
              </h2>
              <button
                onClick={handleCancel}
                className="min-h-[44px] min-w-[44px] flex items-center justify-center text-cloistr-text-muted hover:text-cloistr-text"
              >
                ✕
              </button>
            </div>

            <div className="p-4 space-y-4">
              {/* Name */}
              <div>
                <label className="block text-sm font-medium mb-1">Filter name</label>
                <input
                  type="text"
                  value={editing.name}
                  onChange={e => update({ name: e.target.value })}
                  placeholder="e.g. Tag newsletters"
                  className="w-full px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary text-sm"
                />
              </div>

              <fieldset>
                <legend className="text-sm font-medium mb-2">Match criteria (all checked fields must match)</legend>
                <div className="space-y-2">
                  <div>
                    <label className="block text-xs text-cloistr-text-muted mb-1">From</label>
                    <input
                      type="text"
                      value={editing.from}
                      onChange={e => update({ from: e.target.value })}
                      placeholder="e.g. newsletter@example.com"
                      className="w-full px-3 py-2 border border-cloistr-border rounded-lg text-sm"
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-cloistr-text-muted mb-1">To</label>
                    <input
                      type="text"
                      value={editing.to}
                      onChange={e => update({ to: e.target.value })}
                      placeholder="e.g. me@cloistr.xyz"
                      className="w-full px-3 py-2 border border-cloistr-border rounded-lg text-sm"
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-cloistr-text-muted mb-1">Subject contains</label>
                    <input
                      type="text"
                      value={editing.subject}
                      onChange={e => update({ subject: e.target.value })}
                      placeholder="e.g. Newsletter"
                      className="w-full px-3 py-2 border border-cloistr-border rounded-lg text-sm"
                    />
                  </div>
                  <label className="flex items-center gap-2 min-h-[44px] cursor-pointer">
                    <input
                      type="checkbox"
                      checked={editing.hasAttachment}
                      onChange={e => update({ hasAttachment: e.target.checked })}
                      className="w-4 h-4"
                    />
                    <span className="text-sm">Has attachment</span>
                  </label>
                </div>
              </fieldset>

              <fieldset>
                <legend className="text-sm font-medium mb-2">Action</legend>
                <div className="space-y-2">
                  <select
                    value={editing.action}
                    onChange={e => update({ action: e.target.value as FilterRule['action'] })}
                    className="w-full px-3 py-2 border border-cloistr-border rounded-lg text-sm"
                  >
                    <option value="label">Apply label</option>
                    <option value="move">Move to folder</option>
                    <option value="star">Star</option>
                    <option value="markRead">Mark as read</option>
                  </select>

                  {(editing.action === 'label' || editing.action === 'move') && (
                    <input
                      type="text"
                      value={editing.actionValue}
                      onChange={e => update({ actionValue: e.target.value })}
                      placeholder={editing.action === 'label' ? 'Label name' : 'Folder name'}
                      className="w-full px-3 py-2 border border-cloistr-border rounded-lg text-sm"
                    />
                  )}
                </div>
              </fieldset>
            </div>

            <div className="p-4 border-t flex gap-2 flex-wrap">
              <button
                onClick={handleSave}
                className="min-h-[44px] flex-1 px-4 py-2 bg-cloistr-primary text-white rounded-lg hover:bg-cloistr-primary-hover transition text-sm"
              >
                Save filter
              </button>
              <button
                onClick={handleCancel}
                className="min-h-[44px] px-4 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition text-sm"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
