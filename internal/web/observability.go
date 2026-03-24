package web

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type requestTrace struct {
	name   string
	start  time.Time
	fields []string
	steps  []traceStep
}

type traceStep struct {
	name     string
	duration time.Duration
}

func newRequestTrace(name string) *requestTrace {
	return &requestTrace{
		name:  name,
		start: time.Now(),
	}
}

func (t *requestTrace) AddField(key string, value interface{}) {
	if t == nil {
		return
	}
	t.fields = append(t.fields, fmt.Sprintf("%s=%v", key, value))
}

func (t *requestTrace) AddStep(name string, started time.Time) {
	if t == nil {
		return
	}
	t.steps = append(t.steps, traceStep{
		name:     name,
		duration: time.Since(started),
	})
}

func (t *requestTrace) Log() {
	if t == nil {
		return
	}

	parts := []string{
		"[perf]",
		fmt.Sprintf("page=%s", t.name),
		fmt.Sprintf("total=%s", time.Since(t.start)),
	}
	parts = append(parts, t.fields...)

	if len(t.steps) > 0 {
		stepParts := make([]string, 0, len(t.steps))
		for _, step := range t.steps {
			stepParts = append(stepParts, fmt.Sprintf("%s=%s", step.name, step.duration))
		}
		parts = append(parts, "steps="+strings.Join(stepParts, ","))
	}

	log.Print(strings.Join(parts, " "))
}
