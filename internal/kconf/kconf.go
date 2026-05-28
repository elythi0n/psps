// Package kconf reads and writes kitty.conf while preserving comments and ordering.
//
// The model is line-based. Each line is classified once on parse; mutations
// edit lines in place or append new ones. Writing emits the lines back as-is.
package kconf

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type LineKind int

const (
	LineBlank LineKind = iota
	LineComment
	LineDirective // key value
	LineMap       // map <chord> <action...>
)

type Line struct {
	Kind  LineKind
	Raw   string
	Key   string // directive key or "" / for map: the chord
	Value string // directive value / for map: the action
}

type Config struct {
	Path  string
	Lines []Line
}

func Default() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h + "/.config/kitty/kitty.conf"
	}
	return "kitty.conf"
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	c := &Config{Path: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		c.Lines = append(c.Lines, classify(sc.Text()))
	}
	return c, sc.Err()
}

func classify(raw string) Line {
	t := strings.TrimSpace(raw)
	if t == "" {
		return Line{Kind: LineBlank, Raw: raw}
	}
	if strings.HasPrefix(t, "#") {
		return Line{Kind: LineComment, Raw: raw}
	}
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return Line{Kind: LineBlank, Raw: raw}
	}
	if fields[0] == "map" && len(fields) >= 3 {
		return Line{
			Kind:  LineMap,
			Raw:   raw,
			Key:   fields[1],
			Value: strings.Join(fields[2:], " "),
		}
	}
	if len(fields) >= 2 {
		return Line{
			Kind:  LineDirective,
			Raw:   raw,
			Key:   fields[0],
			Value: strings.Join(fields[1:], " "),
		}
	}
	return Line{Kind: LineDirective, Raw: raw, Key: fields[0]}
}

// Get returns the value of the last directive matching key, or "".
func (c *Config) Get(key string) string {
	for i := len(c.Lines) - 1; i >= 0; i-- {
		l := c.Lines[i]
		if l.Kind == LineDirective && l.Key == key {
			return l.Value
		}
	}
	return ""
}

// Set replaces the last occurrence of key, or appends if missing.
func (c *Config) Set(key, value string) {
	newRaw := fmt.Sprintf("%s %s", key, value)
	for i := len(c.Lines) - 1; i >= 0; i-- {
		l := c.Lines[i]
		if l.Kind == LineDirective && l.Key == key {
			c.Lines[i] = Line{Kind: LineDirective, Raw: newRaw, Key: key, Value: value}
			return
		}
	}
	c.Lines = append(c.Lines, Line{Kind: LineDirective, Raw: newRaw, Key: key, Value: value})
}

// SetMany applies multiple Set calls in order.
func (c *Config) SetMany(pairs map[string]string) {
	for k, v := range pairs {
		c.Set(k, v)
	}
}

// SetMap replaces the last `map <chord> ...` directive for the given chord,
// or appends a new one if none exists. Used by profile install / keybinds.
func (c *Config) SetMap(chord, action string) {
	newRaw := fmt.Sprintf("map %s %s", chord, action)
	for i := len(c.Lines) - 1; i >= 0; i-- {
		l := c.Lines[i]
		if l.Kind == LineMap && l.Key == chord {
			c.Lines[i] = Line{Kind: LineMap, Raw: newRaw, Key: chord, Value: action}
			return
		}
	}
	c.Lines = append(c.Lines, Line{Kind: LineMap, Raw: newRaw, Key: chord, Value: action})
}

// Maps returns all map directives in order.
func (c *Config) Maps() []Line {
	var out []Line
	for _, l := range c.Lines {
		if l.Kind == LineMap {
			out = append(out, l)
		}
	}
	return out
}

// Save writes the config atomically (write to tmp, rename).
func (c *Config) Save() error {
	return c.SaveAs(c.Path)
}

func (c *Config) SaveAs(path string) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, l := range c.Lines {
		if _, err := w.WriteString(l.Raw); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
