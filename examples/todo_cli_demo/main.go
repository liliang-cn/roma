package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type todo struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type todoFile struct {
	NextID int    `json:"next_id"`
	Items  []todo `json:"items"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	filePath := ".todo.json"
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--file" && i+1 < len(args) {
			filePath = args[i+1]
			i++
		} else {
			rest = append(rest, args[i])
		}
	}

	if len(rest) == 0 {
		fmt.Fprintf(stderr, "Usage: todo <command> [args]\n\nCommands:\n  add <title>   Add a new todo\n  list          List all todos\n  done <id>     Mark a todo as done\n  remove <id>   Remove a todo\n")
		return 1
	}

	store, _ := loadTodos(filePath)

	switch rest[0] {
	case "add":
		if len(rest) < 2 {
			return 1
		}
		title := strings.TrimSpace(strings.Join(rest[1:], " "))
		if title == "" {
			return 1
		}
		item := todo{ID: store.NextID, Title: title}
		store.NextID++
		store.Items = append(store.Items, item)
		saveTodos(filePath, store)
		fmt.Fprintf(stdout, "added %d\n", item.ID)
		return 0
	case "list":
		if len(store.Items) == 0 {
			fmt.Fprintln(stdout, "no todos")
			return 0
		}
		for _, item := range store.Items {
			mark := " "
			if item.Done {
				mark = "x"
			}
			fmt.Fprintf(stdout, "[%s] %d %s\n", mark, item.ID, item.Title)
		}
		return 0
	case "done":
		if len(rest) < 2 {
			return 1
		}
		id, err := strconv.Atoi(rest[1])
		if err != nil {
			return 1
		}
		for i := range store.Items {
			if store.Items[i].ID == id {
				store.Items[i].Done = true
				saveTodos(filePath, store)
				fmt.Fprintf(stdout, "completed %d\n", id)
				return 0
			}
		}
		return 1
	case "remove":
		if len(rest) < 2 {
			return 1
		}
		id, err := strconv.Atoi(rest[1])
		if err != nil {
			return 1
		}
		for i := range store.Items {
			if store.Items[i].ID == id {
				store.Items = append(store.Items[:i], store.Items[i+1:]...)
				saveTodos(filePath, store)
				fmt.Fprintf(stdout, "removed %d\n", id)
				return 0
			}
		}
		return 1
	default:
		return 1
	}
}

func loadTodos(path string) (todoFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return todoFile{NextID: 1, Items: []todo{}}, nil
	}
	var data todoFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return todoFile{NextID: 1, Items: []todo{}}, nil
	}
	if data.NextID <= 0 {
		data.NextID = 1
		for _, item := range data.Items {
			if item.ID >= data.NextID {
				data.NextID = item.ID + 1
			}
		}
	}
	return data, nil
}

func saveTodos(path string, data todoFile) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	raw, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(path, raw, 0644)
}
