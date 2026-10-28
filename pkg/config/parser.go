package config

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// SectionData represents key-value pairs in a section
type SectionData map[string]string

// ParsedINI stores the global and section-level key-value mappings
type ParsedINI struct {
	Global   SectionData
	Sections map[string]SectionData
}

// parseINI parses simple INI / key-value configuration text.
// Supports:
// - Comments starting with #, //, or ;
// - Sections [section_name]
// - key = value or key: value
// - Optional quotes around values (e.g. key = "value")
func parseINI(content []byte) *ParsedINI {
	result := &ParsedINI{
		Global:   make(SectionData),
		Sections: make(map[string]SectionData),
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	currentSection := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "//") {
			continue
		}

		// Section header [section_name]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if _, exists := result.Sections[currentSection]; !exists {
				result.Sections[currentSection] = make(SectionData)
			}
			continue
		}

		// Key-value pair
		sepIdx := strings.IndexAny(line, "=:")
		if sepIdx < 0 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(line[:sepIdx]))
		val := strings.TrimSpace(line[sepIdx+1:])

		// Strip inline comment if any: e.g. val # comment
		if commentIdx := strings.IndexAny(val, "#;"); commentIdx > 0 {
			val = strings.TrimSpace(val[:commentIdx])
		}

		// Strip surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}

		if currentSection == "" {
			result.Global[key] = val
		} else {
			result.Sections[currentSection][key] = val
		}
	}

	return result
}

// Helper methods to read typed values
func (ini *ParsedINI) Get(section, key string) (string, bool) {
	key = strings.ToLower(key)
	if section != "" {
		if sec, ok := ini.Sections[strings.ToLower(section)]; ok {
			if v, ok := sec[key]; ok {
				return v, true
			}
		}
	}
	v, ok := ini.Global[key]
	return v, ok
}

func (ini *ParsedINI) GetString(section, key, defaultVal string) string {
	if v, ok := ini.Get(section, key); ok && v != "" {
		return v
	}
	return defaultVal
}

func (ini *ParsedINI) GetInt(section, key string, defaultVal int) int {
	if v, ok := ini.Get(section, key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}

func (ini *ParsedINI) GetInt64(section, key string, defaultVal int64) int64 {
	if v, ok := ini.Get(section, key); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return defaultVal
}

func (ini *ParsedINI) GetBool(section, key string, defaultVal bool) bool {
	if v, ok := ini.Get(section, key); ok {
		v = strings.ToLower(v)
		if v == "true" || v == "1" || v == "yes" || v == "on" {
			return true
		}
		if v == "false" || v == "0" || v == "no" || v == "off" {
			return false
		}
	}
	return defaultVal
}

func (ini *ParsedINI) GetStringSlice(section, key string, defaultVal []string) []string {
	if v, ok := ini.Get(section, key); ok {
		v = strings.Trim(v, "[]")
		parts := strings.Split(v, ",")
		res := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			p = strings.Trim(p, "\"'")
			if p != "" {
				res = append(res, p)
			}
		}
		if len(res) > 0 {
			return res
		}
	}
	return defaultVal
}

func (ini *ParsedINI) GetIntSlice(section, key string, defaultVal []int) []int {
	if v, ok := ini.Get(section, key); ok {
		v = strings.Trim(v, "[]")
		parts := strings.Split(v, ",")
		res := make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if n, err := strconv.Atoi(p); err == nil {
				res = append(res, n)
			}
		}
		if len(res) > 0 {
			return res
		}
	}
	return defaultVal
}
