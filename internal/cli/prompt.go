package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Prompter struct {
	reader *bufio.Reader
	out    io.Writer
}

func NewPrompter(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{
		reader: bufio.NewReader(in),
		out:    out,
	}
}

func (p *Prompter) Select(label string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("there are no options for %s", label)
	}
	fmt.Fprintf(p.out, "\n%s:\n", label)
	for index, option := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", index+1, option)
	}

	for {
		answer, err := p.Text("Choose an option", "1")
		if err != nil {
			return 0, err
		}
		choice, err := strconv.Atoi(answer)
		if err == nil && choice >= 1 && choice <= len(options) {
			return choice - 1, nil
		}
		fmt.Fprintf(p.out, "Enter a number between 1 and %d.\n", len(options))
	}
}

func (p *Prompter) Text(label, defaultValue string) (string, error) {
	prompt := label + ": "
	if defaultValue != "" {
		prompt = fmt.Sprintf("%s [%s]: ", label, defaultValue)
	}
	fmt.Fprint(p.out, prompt)
	answer, err := p.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = defaultValue
	}
	return answer, nil
}

func (p *Prompter) Port(label string, defaultPort int) (int, error) {
	for {
		answer, err := p.Text(label, strconv.Itoa(defaultPort))
		if err != nil {
			return 0, err
		}
		port, err := strconv.Atoi(answer)
		if err == nil && port >= 1 && port <= 65535 {
			return port, nil
		}
		fmt.Fprintln(p.out, "Enter a port between 1 and 65535.")
	}
}

func (p *Prompter) Confirm(label string, defaultValue bool) (bool, error) {
	defaultText := "y"
	if !defaultValue {
		defaultText = "n"
	}
	for {
		answer, err := p.Text(label+" (y/n)", defaultText)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(p.out, "Enter y or n.")
		}
	}
}
