package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Task represents a todo item in the system
type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Completed   bool      `json:"completed"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateTaskRequest is the payload for creating a new task
type CreateTaskRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
}

// UpdateTaskRequest is the payload for updating an existing task
type UpdateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Completed   *bool  `json:"completed"`
}

// TaskRepository handles in-memory task storage with thread-safe operations
type TaskRepository struct {
	mu    sync.RWMutex
	tasks map[string]Task
}

// NewTaskRepository creates a new in-memory task repository
func NewTaskRepository() *TaskRepository {
	return &TaskRepository{
		tasks: make(map[string]Task),
	}
}

// GetAll returns all tasks
func (r *TaskRepository) GetAll() []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tasks := make([]Task, 0, len(r.tasks))
	for _, task := range r.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// GetByID returns a task by ID, returns nil if not found
func (r *TaskRepository) GetByID(id string) *Task {
	r.mu.RLock()
	defer r.mu.RUnlock()

	task, exists := r.tasks[id]
	if !exists {
		return nil
	}
	return &task
}

// Create adds a new task and returns it
func (r *TaskRepository) Create(req CreateTaskRequest) Task {
	r.mu.Lock()
	defer r.mu.Unlock()

	task := Task{
		ID:          uuid.New().String(),
		Title:       req.Title,
		Description: req.Description,
		Completed:   false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	r.tasks[task.ID] = task
	return task
}

// Update modifies an existing task, returns false if not found
func (r *TaskRepository) Update(id string, req UpdateTaskRequest) (*Task, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	task, exists := r.tasks[id]
	if !exists {
		return nil, false
	}

	if req.Title != "" {
		task.Title = req.Title
	}
	if req.Description != "" {
		task.Description = req.Description
	}
	if req.Completed != nil {
		task.Completed = *req.Completed
	}
	task.UpdatedAt = time.Now()

	r.tasks[id] = task
	return &task, true
}

// Delete removes a task, returns false if not found
func (r *TaskRepository) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, exists := r.tasks[id]
	if !exists {
		return false
	}
	delete(r.tasks, id)
	return true
}

// handlers

// getTasks handles GET /api/tasks - returns all tasks
func getTasks(c *gin.Context, repo *TaskRepository) {
	tasks := repo.GetAll()
	if tasks == nil {
		tasks = []Task{}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

// getTask handles GET /api/tasks/:id - returns a single task
func getTask(c *gin.Context, repo *TaskRepository) {
	id := c.Param("id")
	task := repo.GetByID(id)

	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	c.JSON(http.StatusOK, task)
}

// createTask handles POST /api/tasks - creates a new task
func createTask(c *gin.Context, repo *TaskRepository) {
	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	task := repo.Create(req)
	c.JSON(http.StatusCreated, task)
}

// updateTask handles PUT /api/tasks/:id - updates an existing task
func updateTask(c *gin.Context, repo *TaskRepository) {
	id := c.Param("id")

	var req UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	task, ok := repo.Update(id, req)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	c.JSON(http.StatusOK, task)
}

// deleteTask handles DELETE /api/tasks/:id - deletes a task
func deleteTask(c *gin.Context, repo *TaskRepository) {
	id := c.Param("id")

	ok := repo.Delete(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

func main() {
	// Set Gin to release mode for production
	gin.SetMode(gin.ReleaseMode)

	// Create router with default middleware (logger + recovery)
	r := gin.Default()

	// Initialize repository
	repo := NewTaskRepository()

	// API routes group
	api := r.Group("/api")
	{
		api.GET("/tasks", func(c *gin.Context) {
			page := c.DefaultQuery("page", "1")
			limit := c.DefaultQuery("limit", "10")

			pageNum, _ := strconv.Atoi(page)
			limitNum, _ := strconv.Atoi(limit)

			// Get all tasks for simple pagination
			allTasks := repo.GetAll()
			start := (pageNum - 1) * limitNum
			end := start + limitNum

			if start >= len(allTasks) {
				c.JSON(http.StatusOK, gin.H{
					"tasks":      []Task{},
					"page":       pageNum,
					"limit":      limitNum,
					"total":      len(allTasks),
					"totalPages": (len(allTasks) + limitNum - 1) / limitNum,
				})
				return
			}

			if end > len(allTasks) {
				end = len(allTasks)
			}

			paginatedTasks := allTasks[start:end]

			c.JSON(http.StatusOK, gin.H{
				"tasks":      paginatedTasks,
				"page":       pageNum,
				"limit":      limitNum,
				"total":      len(allTasks),
				"totalPages": (len(allTasks) + limitNum - 1) / limitNum,
			})
		})
		api.GET("/tasks/:id", func(c *gin.Context) { getTask(c, repo) })
		api.POST("/tasks", func(c *gin.Context) { createTask(c, repo) })
		api.PUT("/tasks/:id", func(c *gin.Context) { updateTask(c, repo) })
		api.DELETE("/tasks/:id", func(c *gin.Context) { deleteTask(c, repo) })
	}

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Start server on port 8080
	r.Run(":8080")
}