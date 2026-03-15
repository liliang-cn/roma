import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import App from './App.jsx'

function installFetchMock() {
  let nextID = 1
  let todos = []

  global.fetch = vi.fn(async (input, init = {}) => {
    const method = init.method ?? 'GET'
    const url = typeof input === 'string' ? input : input.toString()

    if (url === '/api/todos' && method === 'GET') {
      return jsonResponse(todos)
    }
    if (url === '/api/todos' && method === 'POST') {
      const payload = JSON.parse(init.body)
      const todo = {
        id: String(nextID++),
        title: payload.title,
        completed: false,
        createdAt: new Date().toISOString(),
      }
      todos = [...todos, todo]
      return jsonResponse(todo, 201)
    }
    if (url.startsWith('/api/todos/') && method === 'PATCH') {
      const id = url.split('/').pop()
      todos = todos.map((todo) => (todo.id === id ? { ...todo, completed: !todo.completed } : todo))
      return jsonResponse(todos.find((todo) => todo.id === id))
    }
    if (url.startsWith('/api/todos/') && method === 'DELETE') {
      const id = url.split('/').pop()
      todos = todos.filter((todo) => todo.id !== id)
      return new Response(null, { status: 204 })
    }
    if (url === '/api/todos?completed=true' && method === 'DELETE') {
      const removed = todos.filter((todo) => todo.completed).length
      todos = todos.filter((todo) => !todo.completed)
      return jsonResponse({ removed })
    }
    if (url === '/api/suggest' && method === 'POST') {
      const payload = JSON.parse(init.body)
      return jsonResponse({
        suggestions: [
          `Define clear learning objectives (for: ${payload.goal})`,
          `Read the Go tour (for: ${payload.goal})`,
        ],
      })
    }
    return new Response('not found', { status: 404 })
  })
}

function jsonResponse(payload, status = 200) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

beforeEach(() => {
  installFetchMock()
})

afterEach(() => {
  vi.restoreAllMocks()
})

test('adds and toggles a todo', async () => {
  const user = userEvent.setup()
  render(<App />)

  await waitFor(() => expect(screen.getByText('No todos yet. Add one or generate some from a goal.')).toBeInTheDocument())

  await user.type(screen.getByLabelText('new todo title'), 'Write tests')
  await user.click(screen.getByRole('button', { name: 'Add Todo' }))

  const list = screen.getByLabelText('todo list')
  expect(await within(list).findByText('Write tests')).toBeInTheDocument()

  await user.click(screen.getByLabelText('toggle Write tests'))
  await waitFor(() => expect(screen.getByText('0 remaining')).toBeInTheDocument())
})

test('converts suggestions into todos and clears completed items', async () => {
  const user = userEvent.setup()
  render(<App />)

  await user.type(screen.getByLabelText('goal input'), 'learn Go programming')
  await user.click(screen.getByRole('button', { name: 'Suggest Todos' }))

  const suggestions = await screen.findByLabelText('suggestions')
  expect(within(suggestions).getByText(/Define clear learning objectives/)).toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: 'Add Suggested Todos' }))
  expect(await screen.findByText(/Define clear learning objectives/)).toBeInTheDocument()

  await user.click(screen.getByLabelText(/toggle Define clear learning objectives/))
  await user.click(screen.getByRole('button', { name: 'Clear Completed' }))

  await waitFor(() => {
    expect(screen.queryByText(/Define clear learning objectives/)).not.toBeInTheDocument()
  })
})
