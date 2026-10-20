package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeDatabase struct {
	closeCalls int
	closeErr   error
	values     map[string]string
	setErr     error
	getErr     error
	deleteErr  error
}

func (db *fakeDatabase) Set(key, value []byte) error {
	if db.setErr != nil {
		return db.setErr
	}
	if db.values == nil {
		db.values = make(map[string]string)
	}
	db.values[string(key)] = string(value)
	return nil
}

func (db *fakeDatabase) Get(key []byte) ([]byte, bool, error) {
	if db.getErr != nil {
		return nil, false, db.getErr
	}
	value, ok := db.values[string(key)]
	return []byte(value), ok, nil
}

func (db *fakeDatabase) Delete(key []byte) error {
	if db.deleteErr != nil {
		return db.deleteErr
	}
	delete(db.values, string(key))
	return nil
}

func (db *fakeDatabase) Close() error {
	db.closeCalls++
	return db.closeErr
}

func TestRunREPLHelpAndExit(t *testing.T) {
	db := new(fakeDatabase)
	var output strings.Builder

	err := runREPL(db, strings.NewReader("HELP\nEXIT\n"), &output)
	if err != nil {
		t.Fatalf("runREPL() error = %v", err)
	}
	if db.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", db.closeCalls)
	}
	result := output.String()
	for _, want := range []string{"kminv> ", "Commands:", "SET <key> <value>", "GET <key>", "DELETE <key>", "HAS <key>", "EXIT", "QUIT", "Goodbye."} {
		if !strings.Contains(result, want) {
			t.Fatalf("output missing %q:\n%s", want, result)
		}
	}
}

func TestRunREPLInvalidCommandReturnsToPrompt(t *testing.T) {
	db := new(fakeDatabase)
	var output strings.Builder

	err := runREPL(db, strings.NewReader("NOPE\nQUIT\n"), &output)
	if err != nil {
		t.Fatalf("runREPL() error = %v", err)
	}
	if db.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", db.closeCalls)
	}
	if !strings.Contains(output.String(), `unknown command "NOPE"`) {
		t.Fatalf("output = %q, want helpful unknown-command message", output.String())
	}
	if strings.Count(output.String(), "kminv> ") != 2 {
		t.Fatalf("prompt count = %d, want 2", strings.Count(output.String(), "kminv> "))
	}
}

func TestRunREPLClosesOnEOF(t *testing.T) {
	db := new(fakeDatabase)
	var output strings.Builder

	err := runREPL(db, strings.NewReader("HELP\n"), &output)
	if err != nil {
		t.Fatalf("runREPL() error = %v", err)
	}
	if db.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", db.closeCalls)
	}
}

func TestRunREPLReportsCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	db := &fakeDatabase{closeErr: closeErr}

	err := runREPL(db, strings.NewReader("EXIT\n"), &strings.Builder{})
	if !errors.Is(err, closeErr) {
		t.Fatalf("runREPL() error = %v, want close error", err)
	}
}

func TestRunREPLDatabaseCommands(t *testing.T) {
	db := &fakeDatabase{values: make(map[string]string)}
	var output strings.Builder

	err := runREPL(db, strings.NewReader("set greeting \"hello world\"\nGET greeting\nHAS greeting\nDELETE greeting\nGET greeting\nHAS greeting\nQUIT\n"), &output)
	if err != nil {
		t.Fatalf("runREPL() error = %v", err)
	}
	result := output.String()
	for _, want := range []string{"OK", "hello world", "true", "Key not found", "false"} {
		if !strings.Contains(result, want) {
			t.Fatalf("output missing %q:\n%s", want, result)
		}
	}
	if db.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", db.closeCalls)
	}
}

func TestRunREPLSetOverwriteAndEmptyQuotedArguments(t *testing.T) {
	db := &fakeDatabase{values: make(map[string]string)}
	var output strings.Builder

	err := runREPL(db, strings.NewReader("SET key old\nSET key new\nGET key\nSET \"\" \"\"\nGET \"\"\nEXIT\n"), &output)
	if err != nil {
		t.Fatalf("runREPL() error = %v", err)
	}
	if !strings.Contains(output.String(), "new\n") {
		t.Fatalf("output = %q, want overwritten value", output.String())
	}
	if db.values[""] != "" {
		t.Fatalf("empty key value = %q, want empty value", db.values[""])
	}
}

func TestRunREPLCommandErrorsContinue(t *testing.T) {
	db := &fakeDatabase{
		values:    make(map[string]string),
		setErr:    errors.New("set failed"),
		getErr:    errors.New("get failed"),
		deleteErr: errors.New("delete failed"),
	}
	var output strings.Builder

	err := runREPL(db, strings.NewReader("SET key value\nGET key\nDELETE key\nSET only\nSET greeting hello world\nSET greeting \"unterminated\nEXIT\n"), &output)
	if err != nil {
		t.Fatalf("runREPL() error = %v", err)
	}
	result := output.String()
	for _, want := range []string{"SET failed", "GET failed", "DELETE failed", "usage: SET <key> <value>", "unterminated double quote"} {
		if !strings.Contains(result, want) {
			t.Fatalf("output missing %q:\n%s", want, result)
		}
	}
	if db.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", db.closeCalls)
	}
}

func TestRunREPLClosesOnInputError(t *testing.T) {
	db := new(fakeDatabase)
	var output strings.Builder
	inputErr := errors.New("input failed")

	err := runREPL(db, errorReader{err: inputErr}, &output)
	if !errors.Is(err, inputErr) {
		t.Fatalf("runREPL() error = %v, want input error", err)
	}
	if db.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", db.closeCalls)
	}
}

func TestParseArguments(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{line: `SET greeting "hello world"`, want: []string{"SET", "greeting", "hello world"}},
		{line: `SET "" ""`, want: []string{"SET", "", ""}},
		{line: "  GET key  ", want: []string{"GET", "key"}},
	}
	for _, test := range tests {
		got, err := parseArguments(test.line)
		if err != nil {
			t.Fatalf("parseArguments(%q) error = %v", test.line, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Errorf("parseArguments(%q) = %q, want %q", test.line, got, test.want)
		}
	}
	if _, err := parseArguments(`SET key "unterminated`); err == nil {
		t.Fatal("parseArguments() error = nil, want unterminated quote error")
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
