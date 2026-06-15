# REST API Design - Todo Application

## Framework
**Gin** (github.com/gin-gonic/gin)

## Project Structure
```
todo-api/
├── main.go
├── go.mod
├── internal/
│   ├── handlers/
│   │   └── todo.go
│   ├── models/
│   │   └── todo.go
│   └── repository/
│       └── todo.go
```

## API Endpoints

### Tasks
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /api/tasks | List all tasks |
| GET | /api/tasks/:id | Get task by ID |
| POST | /api/tasks | Create new task |
| PUT | /api/tasks/:id | Update task |
| DELETE | /api/tasks/:id | Delete task |

## Data Models

### Task
```go
type Task struct {
    ID          string    `json:"id"`
    Title       string    `json:"title" binding:"required"`
    Description string    `json:"description"`
    Completed   bool      `json:"completed"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

### CreateTaskRequest
```go
type CreateTaskRequest struct {
    Title       string `json:"title" binding:"required"`
    Description string `json:"description"`
}
```

### UpdateTaskRequest
```go
type UpdateTaskRequest struct {
    Title       string `json:"title"`
    Description string `json:"description"`
    Completed   *bool  `json:"completed"`
}
```

## Key Design Decisions
1. In-memory storage for simplicity (can swap with database)
2. UUID for task IDs
3. RESTful JSON responses
4. Standard HTTP status codes
5. Middleware for logging and recovery
6. Validation using Gin's binding