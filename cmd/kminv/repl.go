package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

type database interface {
	Set([]byte, []byte) error
	Get([]byte) ([]byte, bool, error)
	Delete([]byte) error
	Close() error
}

func runREPL(db database, input io.Reader, output io.Writer) (resultErr error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	closed := false
	finish := func(err error) error {
		if closed {
			return err
		}
		closed = true
		return errors.Join(err, closeDatabase(db))
	}

	for {
		if _, err := fmt.Fprint(output, "kminv> "); err != nil {
			return finish(fmt.Errorf("write prompt: %w", err))
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return finish(fmt.Errorf("read input: %w", err))
			}
			if _, err := fmt.Fprintln(output); err != nil {
				return finish(fmt.Errorf("write output: %w", err))
			}
			return finish(nil)
		}

		args, err := parseArguments(scanner.Text())
		if err != nil {
			if err := writeCommandError(output, err); err != nil {
				return finish(err)
			}
			continue
		}
		if len(args) == 0 {
			continue
		}

		switch strings.ToUpper(args[0]) {
		case "HELP":
			if len(args) != 1 {
				err = fmt.Errorf("HELP takes no arguments")
			} else {
				err = writeHelp(output)
			}
		case "EXIT", "QUIT":
			if len(args) != 1 {
				err = fmt.Errorf("%s takes no arguments", strings.ToUpper(args[0]))
			} else {
				if _, writeErr := fmt.Fprintln(output, "Goodbye."); writeErr != nil {
					err = fmt.Errorf("write output: %w", writeErr)
				} else {
					return finish(nil)
				}
			}
		case "SET":
			err = setCommand(db, output, args)
		case "GET":
			err = getCommand(db, output, args)
		case "DELETE":
			err = deleteCommand(db, output, args)
		case "HAS":
			err = hasCommand(db, output, args)
		default:
			err = fmt.Errorf("unknown command %q; type HELP for available commands", args[0])
		}
		if err != nil {
			if writeErr := writeCommandError(output, err); writeErr != nil {
				return finish(writeErr)
			}
		}
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
	_, err := fmt.Fprintln(output,
		"Commands:",
		"  SET <key> <value>   Store a value",
		"  GET <key>           Display a value",
		"  DELETE <key>       Delete a key",
		"  HAS <key>           Check whether a key exists",
		"  HELP                Show this help message",
		"  EXIT                Close the database and exit",
		"  QUIT                Close the database and exit",
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
