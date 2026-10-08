package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

const DefaultMaxFramesPerGroup = 32

var goroutineHeader = regexp.MustCompile(`^goroutine \d+ \[([^\]]+)\]:$`)

type StackGroup struct {
	Signature               string   `json:"signature"`
	Count                   int      `json:"count"`
	State                   string   `json:"state"`
	Frames                  []string `json:"frames"`
	ContainsIntentionalLeak bool     `json:"contains_intentional_leak"`
}

type ParsedProfile struct {
	TotalGoroutines int          `json:"total_goroutines"`
	Groups          []StackGroup `json:"-"`
}

type parsedGoroutine struct {
	state  string
	frames []string
}

func ParseGoroutineProfile(profile []byte, maxFrames int) ParsedProfile {
	if maxFrames < 1 {
		maxFrames = DefaultMaxFramesPerGroup
	}
	lines := strings.Split(strings.ReplaceAll(string(profile), "\r\n", "\n"), "\n")
	var goroutines []parsedGoroutine
	var current *parsedGoroutine
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if match := goroutineHeader.FindStringSubmatch(line); match != nil {
			state := strings.TrimSpace(strings.SplitN(match[1], ",", 2)[0])
			goroutines = append(goroutines, parsedGoroutine{state: state})
			current = &goroutines[len(goroutines)-1]
			continue
		}
		if current == nil || line == "" || strings.HasPrefix(rawLine, "\t") {
			continue
		}
		frame := normalizeFunctionFrame(line)
		if frame == "" || len(current.frames) >= maxFrames {
			continue
		}
		current.frames = append(current.frames, frame)
	}

	grouped := make(map[string]*StackGroup)
	for _, goroutine := range goroutines {
		key := goroutine.state + "\n" + strings.Join(goroutine.frames, "\n")
		group, exists := grouped[key]
		if !exists {
			hash := sha256.Sum256([]byte(key))
			frames := append([]string(nil), goroutine.frames...)
			group = &StackGroup{
				Signature:               hex.EncodeToString(hash[:8]),
				State:                   goroutine.state,
				Frames:                  frames,
				ContainsIntentionalLeak: containsLeakFrame(frames),
			}
			grouped[key] = group
		}
		group.Count++
	}

	groups := make([]StackGroup, 0, len(grouped))
	for _, group := range grouped {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Signature < groups[j].Signature
	})
	return ParsedProfile{TotalGoroutines: len(goroutines), Groups: groups}
}

func normalizeFunctionFrame(line string) string {
	created := strings.HasPrefix(line, "created by ")
	line = strings.TrimPrefix(line, "created by ")
	if created {
		if index := strings.LastIndex(line, " in goroutine "); index >= 0 {
			line = line[:index]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return ""
		}
		return "created by " + line
	}
	argumentStart := strings.LastIndex(line, "(")
	if argumentStart < 0 {
		return ""
	}
	function := strings.TrimSpace(line[:argumentStart])
	if function == "" {
		return ""
	}
	return function
}

func containsLeakFrame(frames []string) bool {
	for _, frame := range frames {
		if strings.Contains(frame, "LeakController).waitForever") || strings.Contains(frame, "LeakController.waitForever") {
			return true
		}
	}
	return false
}
