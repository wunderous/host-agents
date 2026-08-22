package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

var ErrInputInterrupted = errors.New("input interrupted")

type lineReader interface {
	ReadLine(prompt string, complete func(string) []string) (string, error)
	Close() error
}

type scannerLineReader struct {
	scanner  *bufio.Scanner
	out      io.Writer
	noPrompt bool
}

func (r *scannerLineReader) ReadLine(prompt string, _ func(string) []string) (string, error) {
	if !r.noPrompt {
		_, _ = fmt.Fprint(r.out, prompt)
	}
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return r.scanner.Text(), nil
}

func (r *scannerLineReader) Close() error { return nil }

type terminalLineReader struct {
	in       *os.File
	out      io.Writer
	fd       int
	state    *term.State
	closed   bool
	readByte [1]byte
}

func newLineReader(in io.Reader, out io.Writer, noPrompt bool) (lineReader, error) {
	inFile, inOK := in.(*os.File)
	outFile, outOK := out.(*os.File)
	if !inOK || !outOK || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return &scannerLineReader{scanner: bufio.NewScanner(in), out: out, noPrompt: noPrompt}, nil
	}
	state, err := term.MakeRaw(int(inFile.Fd()))
	if err != nil {
		return &scannerLineReader{scanner: bufio.NewScanner(in), out: out, noPrompt: noPrompt}, nil
	}
	return &terminalLineReader{in: inFile, out: out, fd: int(inFile.Fd()), state: state}, nil
}

func (r *terminalLineReader) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	return term.Restore(r.fd, r.state)
}

func (r *terminalLineReader) ReadLine(prompt string, complete func(string) []string) (string, error) {
	if r == nil || r.closed {
		return "", io.EOF
	}
	buffer := make([]rune, 0, 80)
	cursor := 0
	_, _ = fmt.Fprint(r.out, prompt)
	defer func() { _, _ = fmt.Fprint(r.out, "\r\n") }()

	redraw := func() {
		_, _ = fmt.Fprint(r.out, "\r\033[2K", prompt, string(buffer))
		if remaining := len(buffer) - cursor; remaining > 0 {
			_, _ = fmt.Fprintf(r.out, "\033[%dD", remaining)
		}
	}
	showCompletions := func() {
		if complete == nil {
			return
		}
		candidates := complete(string(buffer))
		if len(candidates) == 0 {
			_, _ = fmt.Fprint(r.out, "\a")
			return
		}
		if len(candidates) == 1 {
			buffer, cursor = replaceCurrentToken(buffer, cursor, candidates[0])
			redraw()
			return
		}
		_, _ = fmt.Fprint(r.out, "\r\n")
		for _, candidate := range candidates {
			_, _ = fmt.Fprintln(r.out, "  "+candidate)
		}
		redraw()
	}

	for {
		b, err := r.readOne()
		if err != nil {
			return "", err
		}
		switch b {
		case '\r', '\n':
			return string(buffer), nil
		case 0x03:
			return "", ErrInputInterrupted
		case 0x04:
			if len(buffer) == 0 {
				return "", io.EOF
			}
		case 0x01:
			cursor = 0
			redraw()
		case 0x05:
			cursor = len(buffer)
			redraw()
		case 0x0c:
			_, _ = fmt.Fprint(r.out, "\033[2J\033[H")
			redraw()
		case '\t':
			showCompletions()
		case 0x7f, 0x08:
			if cursor > 0 {
				buffer = append(buffer[:cursor-1], buffer[cursor:]...)
				cursor--
				redraw()
			}
		case 0x1b:
			action, err := r.readEscape()
			if err != nil {
				return "", err
			}
			switch action {
			case 'D':
				if cursor > 0 {
					cursor--
					redraw()
				}
			case 'C':
				if cursor < len(buffer) {
					cursor++
					redraw()
				}
			case 'H':
				cursor = 0
				redraw()
			case 'F':
				cursor = len(buffer)
				redraw()
			}
		default:
			if b < 0x20 || b == 0x7f {
				continue
			}
			runeValue, err := r.readRune(b)
			if err != nil {
				return "", err
			}
			buffer = append(buffer, 0)
			copy(buffer[cursor+1:], buffer[cursor:])
			buffer[cursor] = runeValue
			cursor++
			redraw()
		}
	}
}

func (r *terminalLineReader) readOne() (byte, error) {
	if _, err := io.ReadFull(r.in, r.readByte[:]); err != nil {
		return 0, err
	}
	return r.readByte[0], nil
}

func (r *terminalLineReader) readEscape() (byte, error) {
	first, err := r.readOne()
	if err != nil {
		return 0, err
	}
	if first != '[' {
		return first, nil
	}
	return r.readOne()
}

func (r *terminalLineReader) readRune(first byte) (rune, error) {
	if first < utf8.RuneSelf {
		return rune(first), nil
	}
	width := utf8.RuneLen(rune(first))
	if width <= 1 || width > utf8.UTFMax {
		return utf8.RuneError, nil
	}
	bytes := make([]byte, width)
	bytes[0] = first
	for index := 1; index < width; index++ {
		value, err := r.readOne()
		if err != nil {
			return 0, err
		}
		bytes[index] = value
	}
	runeValue, _ := utf8.DecodeRune(bytes)
	return runeValue, nil
}

func replaceCurrentToken(buffer []rune, cursor int, replacement string) ([]rune, int) {
	start := cursor
	for start > 0 && !unicode.IsSpace(buffer[start-1]) {
		start--
	}
	end := cursor
	for end < len(buffer) && !unicode.IsSpace(buffer[end]) {
		end++
	}
	result := make([]rune, 0, start+len([]rune(replacement))+len(buffer)-end)
	result = append(result, buffer[:start]...)
	result = append(result, []rune(replacement)...)
	result = append(result, buffer[end:]...)
	return result, start + len([]rune(replacement))
}

func parseSelection(input string, count int) (int, error) {
	input = strings.TrimSpace(input)
	if input == "" || strings.EqualFold(input, "q") || strings.EqualFold(input, "cancel") {
		return -1, ErrInputInterrupted
	}
	value, err := strconv.Atoi(input)
	if err != nil || value < 1 || value > count {
		return -1, fmt.Errorf("select a number from 1 to %d", count)
	}
	return value - 1, nil
}
