package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/peterh/liner"
)

type database interface {
	Set([]byte, []byte) error
	Get([]byte) ([]byte, bool, error)
	Delete([]byte) error
	Close() error
}

// runREPL is the line-oriented fallback used for pipes, redirected input, and tests.
func runREPL(db database, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	return runREPLWithReadLine(db, output, true, func() (string, error) {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", err
			}
			return "", io.EOF
		}
		return scanner.Text(), nil
	})
}

// runInteractiveREPL adds terminal line editing and in-session command history.
// It should only be used when stdin and stdout are attached to a terminal.
func runInteractiveREPL(db database, output io.Writer) error {
	lineEditor := liner.NewLiner()
	lineEditor.SetCtrlCAborts(true)
	defer lineEditor.Close()

	var previousHistoryEntry string
	return runREPLWithReadLine(db, output, false, func() (string, error) {
		line, err := lineEditor.Prompt("kminv> ")
		if err != nil {
			return "", err
		}

		trimmed := strings.TrimSpace(line)
		if trimmed != "" && trimmed != previousHistoryEntry {
			lineEditor.AppendHistory(line)
			previousHistoryEntry = trimmed
		}
		return line, nil
	})
}

func runREPLWithReadLine(db database, output io.Writer, showPrompt bool, readLine func() (string, error)) (resultErr error) {
	closed := false
	finish := func(err error) error {
		if closed {
			return err
		}
		closed = true
		return errors.Join(err, closeDatabase(db))
	}

	for {
		if showPrompt {
			if _, err := fmt.Fprint(output, "kminv> "); err != nil {
				return finish(fmt.Errorf("write prompt: %w", err))
			}
		}

		line, err := readLine()
		if err != nil {
			if errors.Is(err, liner.ErrPromptAborted) {
				if _, writeErr := fmt.Fprintln(output, "^C"); writeErr != nil {
					return finish(fmt.Errorf("write output: %w", writeErr))
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				if _, writeErr := fmt.Fprintln(output); writeErr != nil {
					return finish(fmt.Errorf("write output: %w", writeErr))
				}
				return finish(nil)
			}
			return finish(fmt.Errorf("read input: %w", err))
		}

		exit, commandErr := executeCommand(db, output, line)
		if commandErr != nil {
			if writeErr := writeCommandError(output, commandErr); writeErr != nil {
				return finish(writeErr)
			}
		}
		if exit {
			return finish(nil)
		}
	}
}

func executeCommand(db database, output io.Writer, line string) (bool, error) {
	args, err := parseArguments(line)
	if err != nil {
		return false, err
	}
	if len(args) == 0 {
		return false, nil
	}

	switch strings.ToUpper(args[0]) {
	case "HELP":
		if len(args) != 1 {
			return false, fmt.Errorf("HELP takes no arguments")
		}
		return false, writeHelp(output)
	case "EXIT", "QUIT":
		if len(args) != 1 {
			return false, fmt.Errorf("%s takes no arguments", strings.ToUpper(args[0]))
		}
		if _, err := fmt.Fprintln(output, "Goodbye."); err != nil {
			return false, fmt.Errorf("write output: %w", err)
		}
		return true, nil
	case "SET":
		return false, setCommand(db, output, args)
	case "GET":
		return false, getCommand(db, output, args)
	case "DELETE":
		return false, deleteCommand(db, output, args)
	case "HAS":
		return false, hasCommand(db, output, args)
	default:
		return false, fmt.Errorf("unknown command %q; type HELP for available commands", args[0])
	}
}

func parseArguments(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	inQuote := false
	tokenStarted := false

	for _, character := range line {
		switch {
		case character == '"':
			inQuote = !inQuote
			tokenStarted = true
		case character == ' ' || character == '\t' || character == '\r':
			if inQuote {
				current.WriteRune(character)
			} else if tokenStarted {
				args = append(args, current.String())
				current.Reset()
				tokenStarted = false
			}
		default:
			current.WriteRune(character)
			tokenStarted = true
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated double quote")
	}
	if tokenStarted {
		args = append(args, current.String())
	}
	return args, nil
}

func writeHelp(output io.Writer) error {
	_, err := fmt.Fprint(output,
		"Commands:\n",
		"  SET <key> <value>   Store a value\n",
		"  GET <key>           Display a value\n",
		"  DELETE <key>        Delete a key\n",
		"  HAS <key>           Check whether a key exists\n",
		"  HELP                Show this help message\n",
		"  EXIT                Close the database and exit\n",
		"  QUIT                Close the database and exit\n",
	)
	if err != nil {
		return fmt.Errorf("write help: %w", err)
	}
	return nil
}

func setCommand(db database, output io.Writer, args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: SET <key> <value>")
	}
	if err := db.Set([]byte(args[1]), []byte(args[2])); err != nil {
		return fmt.Errorf("SET failed: %w", err)
	}
	_, err := fmt.Fprintln(output, "OK")
	return err
}

func getCommand(db database, output io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: GET <key>")
	}
	value, ok, err := db.Get([]byte(args[1]))
	if err != nil {
		return fmt.Errorf("GET failed: %w", err)
	}
	if !ok {
		_, err = fmt.Fprintln(output, "Key not found")
		return err
	}
	_, err = fmt.Fprintln(output, string(value))
	return err
}

func deleteCommand(db database, output io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: DELETE <key>")
	}
	if err := db.Delete([]byte(args[1])); err != nil {
		return fmt.Errorf("DELETE failed: %w", err)
	}
	_, err := fmt.Fprintln(output, "OK")
	return err
}

func hasCommand(db database, output io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: HAS <key>")
	}
	_, ok, err := db.Get([]byte(args[1]))
	if err != nil {
		return fmt.Errorf("HAS failed: %w", err)
	}
	_, err = fmt.Fprintln(output, ok)
	return err
}

func writeCommandError(output io.Writer, commandErr error) error {
	if _, err := fmt.Fprintf(output, "Error: %v\n", commandErr); err != nil {
		return fmt.Errorf("write error: %w", err)
	}
	return nil
}

func closeDatabase(db database) error {
	if err := db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}
