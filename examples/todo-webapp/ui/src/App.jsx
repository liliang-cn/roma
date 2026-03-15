import { useEffect, useState } from 'react'

const apiBase = '/api'

async function apiFetch(path, options = {}) {
  const response = await fetch(apiBase + path, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!response.ok) {
    const text = await response.text()
    throw new Error(text || `HTTP ${response.status}`)
  }
  if (response.status === 204) {
    return null
  }
  return response.json()
}

export default function App() {
  const [todos, setTodos] = useState([])
  const [title, setTitle] = useState('')
  const [goal, setGoal] = useState('')
  const [suggestions, setSuggestions] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    async function loadTodos() {
      try {
        const items = await apiFetch('/todos')
        if (!cancelled) {
          setTodos(items ?? [])
          setError('')
        }
      } catch (err) {
        if (!cancelled) {
          setError(err.message)
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }
    loadTodos()
    return () => {
      cancelled = true
    }
  }, [])

  async function addTodo(nextTitle) {
    const todo = await apiFetch('/todos', {
      method: 'POST',
      body: JSON.stringify({ title: nextTitle }),
    })
    setTodos((current) => [...current, todo])
  }

  async function handleSubmit(event) {
    event.preventDefault()
    const nextTitle = title.trim()
    if (!nextTitle) {
      setError('Title is required')
      return
    }
    try {
      await addTodo(nextTitle)
      setTitle('')
      setError('')
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleToggle(id) {
    try {
      const updated = await apiFetch(`/todos/${id}`, { method: 'PATCH' })
      setTodos((current) => current.map((todo) => (todo.id === id ? updated : todo)))
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleDelete(id) {
    try {
      await apiFetch(`/todos/${id}`, { method: 'DELETE' })
      setTodos((current) => current.filter((todo) => todo.id !== id))
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleClearCompleted() {
    try {
      await apiFetch('/todos?completed=true', { method: 'DELETE' })
      setTodos((current) => current.filter((todo) => !todo.completed))
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleSuggest(event) {
    event.preventDefault()
    const nextGoal = goal.trim()
    if (!nextGoal) {
      setError('Goal is required')
      return
    }
    try {
      const payload = await apiFetch('/suggest', {
        method: 'POST',
        body: JSON.stringify({ goal: nextGoal }),
      })
      setSuggestions(payload.suggestions ?? [])
      setError('')
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleAddSuggestions() {
    for (const suggestion of suggestions) {
      // Keep order stable even when adding multiple suggestions.
      // eslint-disable-next-line no-await-in-loop
      await addTodo(suggestion)
    }
    setSuggestions([])
    setGoal('')
  }

  const completedCount = todos.filter((todo) => todo.completed).length
  const remainingCount = todos.length - completedCount

  return (
    <main
      style={{
        minHeight: '100vh',
        display: 'grid',
        placeItems: 'center',
        padding: '40px 16px',
      }}
    >
      <section
        style={{
          width: 'min(760px, 100%)',
          background: 'rgba(255, 255, 255, 0.88)',
          border: '1px solid rgba(22, 50, 79, 0.08)',
          borderRadius: 24,
          boxShadow: '0 24px 70px rgba(22, 50, 79, 0.12)',
          padding: 28,
          backdropFilter: 'blur(12px)',
        }}
      >
        <header style={{ marginBottom: 24 }}>
          <p style={{ margin: 0, color: '#4c7ef3', fontWeight: 700, letterSpacing: '0.08em', textTransform: 'uppercase', fontSize: 12 }}>
            ROMA Demo
          </p>
          <h1 style={{ margin: '8px 0 6px', fontSize: 32 }}>TODO App</h1>
          <p style={{ margin: 0, color: '#5b728c' }}>
            Go API plus React UI, with a deterministic local planner that turns goals into todo suggestions.
          </p>
        </header>

        <form onSubmit={handleSuggest} style={{ display: 'grid', gap: 10, marginBottom: 20 }}>
          <label htmlFor="goal" style={{ fontWeight: 600 }}>Goal to Todos</label>
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
            <input
              id="goal"
              aria-label="goal input"
              value={goal}
              onChange={(event) => setGoal(event.target.value)}
              placeholder="Learn Go programming"
              style={inputStyle}
            />
            <button type="submit" style={primaryButtonStyle}>
              Suggest Todos
            </button>
          </div>
          {suggestions.length > 0 && (
            <div style={panelStyle}>
              <ul aria-label="suggestions" style={{ margin: 0, paddingLeft: 20 }}>
                {suggestions.map((item) => (
                  <li key={item} style={{ marginBottom: 6 }}>{item}</li>
                ))}
              </ul>
              <button type="button" onClick={handleAddSuggestions} style={secondaryButtonStyle}>
                Add Suggested Todos
              </button>
            </div>
          )}
        </form>

        <form onSubmit={handleSubmit} style={{ display: 'flex', gap: 10, flexWrap: 'wrap', marginBottom: 20 }}>
          <input
            aria-label="new todo title"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="What needs to be done?"
            style={inputStyle}
          />
          <button type="submit" style={primaryButtonStyle}>
            Add Todo
          </button>
        </form>

        {error && (
          <p role="alert" style={{ marginTop: 0, color: '#c0392b', fontWeight: 600 }}>
            {error}
          </p>
        )}
        {loading && <p aria-label="loading">Loading…</p>}

        <ul aria-label="todo list" style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {todos.map((todo) => (
            <li
              key={todo.id}
              style={{
                display: 'grid',
                gridTemplateColumns: 'auto 1fr auto',
                alignItems: 'center',
                gap: 12,
                background: '#fff',
                padding: '14px 16px',
                borderRadius: 16,
                border: '1px solid rgba(22, 50, 79, 0.06)',
                marginBottom: 10,
              }}
            >
              <input
                type="checkbox"
                aria-label={`toggle ${todo.title}`}
                checked={todo.completed}
                onChange={() => handleToggle(todo.id)}
              />
              <span
                style={{
                  color: todo.completed ? '#8aa0b8' : '#16324f',
                  textDecoration: todo.completed ? 'line-through' : 'none',
                }}
              >
                {todo.title}
              </span>
              <button
                type="button"
                aria-label={`delete ${todo.title}`}
                onClick={() => handleDelete(todo.id)}
                style={ghostButtonStyle}
              >
                Delete
              </button>
            </li>
          ))}
        </ul>

        {!loading && todos.length === 0 && (
          <p style={{ color: '#5b728c' }}>No todos yet. Add one or generate some from a goal.</p>
        )}

        <footer style={footerStyle}>
          <span>{remainingCount} remaining</span>
          <span>{completedCount} completed</span>
          <button type="button" onClick={handleClearCompleted} style={ghostButtonStyle}>
            Clear Completed
          </button>
        </footer>
      </section>
    </main>
  )
}

const inputStyle = {
  flex: '1 1 280px',
  minWidth: 0,
  padding: '12px 14px',
  borderRadius: 14,
  border: '1px solid rgba(22, 50, 79, 0.14)',
  background: '#fff',
}

const panelStyle = {
  display: 'grid',
  gap: 12,
  padding: 16,
  borderRadius: 16,
  background: 'rgba(76, 126, 243, 0.08)',
}

const primaryButtonStyle = {
  border: 'none',
  borderRadius: 14,
  padding: '12px 16px',
  background: '#16324f',
  color: '#fff',
  cursor: 'pointer',
}

const secondaryButtonStyle = {
  ...primaryButtonStyle,
  background: '#4c7ef3',
}

const ghostButtonStyle = {
  border: '1px solid rgba(22, 50, 79, 0.14)',
  borderRadius: 12,
  padding: '8px 12px',
  background: '#fff',
  color: '#16324f',
  cursor: 'pointer',
}

const footerStyle = {
  display: 'flex',
  gap: 12,
  alignItems: 'center',
  justifyContent: 'space-between',
  flexWrap: 'wrap',
  marginTop: 18,
  color: '#5b728c',
}
