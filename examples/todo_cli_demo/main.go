package main

import (
	"encoding/json"
	"errors"
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
	filePath, rest, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printUsage(stdout)
		return 1
	}
	if len(rest) == 0 {
		printUsage(stdout)
		return 1
	}

	store, err := loadTodos(filePath)
	if err != nil {
		fmt.Fprintf(stderr, "load todos: %v\n", err)
		return 1
	}

	switch rest[0] {
	case "add":
		if len(rest) < 2 || strings.TrimSpace(strings.Join(rest[1:], " ")) == "" {
			fmt.Fprintln(stderr, "add requires todo text")
			return 1
		}
		title := strings.TrimSpace(strings.Join(rest[1:], " "))
		item := todo{ID: store.NextID, Title: title}
		store.NextID++
		store.Items = append(store.Items, item)
		if err := saveTodos(filePath, store); err != nil {
			fmt.Fprintf(stderr, "save todos: %v\n", err)
			return 1
		}
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
		id, err := parseID(rest)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for i := range store.Items {
			if store.Items[i].ID == id {
				store.Items[i].Done = true
				if err := saveTodos(filePath, store); err != nil {
					fmt.Fprintf(stderr, "save todos: %v\n", err)
					return 1
				}
				fmt.Fprintf(stdout, "completed %d\n", id)
				return 0
			}
		}
		fmt.Fprintf(stderr, "todo %d not found\n", id)
		return 1
	case "remove":
		id, err := parseID(rest)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for i := range store.Items {
			if store.Items[i].ID == id {
				store.Items = append(store.Items[:i], store.Items[i+1:]...)
				if err := saveTodos(filePath, store); err != nil {
					fmt.Fprintf(stderr, "save todos: %v\n", err)
					return 1
				}
				fmt.Fprintf(stdout, "removed %d\n", id)
				return 0
			}
		}
		fmt.Fprintf(stderr, "todo %d not found\n", id)
		return 1
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", rest[0])
		printUsage(stdout)
		return 1
	}
}

func parseArgs(args []string) (string, []string, error) {
	filePath := strings.TrimSpace(os.Getenv("TODO_FILE"))
	if filePath == "" {
		filePath = ".todo.json"
	}

	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file":
			i++
			if i >= len(args) {
				return "", nil, errors.New("--file requires a value")
			}
			filePath = args[i]
		default:
			rest = append(rest, args[i])
		}
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("resolve todo file: %w", err)
	}
	return absPath, rest, nil
}

func parseID(args []string) (int, error) {
	if len(args) < 2 {
		return 0, fmt.Errorf("%s requires a numeric id", args[0])
	}
	id, err := strconv.Atoi(args[1])
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%s requires a numeric id", args[0])
	}
	return id, nil
}

func loadTodos(path string) (todoFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return todoFile{NextID: 1, Items: []todo{}}, nil
		}
		return todoFile{}, err
	}

	var data todoFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return todoFile{}, err
	}
	if data.NextID <= 0 {
		data.NextID = nextID(data.Items)
	}
	return data, nil
}

func saveTodos(path string, data todoFile) error {
	if data.NextID <= 0 {
		data.NextID = nextID(data.Items)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func nextID(items []todo) int {
	next := 1
	for _, item := range items {
		if item.ID >= next {
			next = item.ID + 1
		}
	}
	return next
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  todo [--file path] add <text>")
	fmt.Fprintln(w, "  todo [--file path] list")
	fmt.Fprintln(w, "  todo [--file path] done <id>")
	fmt.Fprintln(w, "  todo [--file path] remove <id>")
}
