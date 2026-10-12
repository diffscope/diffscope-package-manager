package download

import (
	"testing"
	"time"
)

func TestProgressUpdatesRemainIndependentAcrossTasks(t *testing.T) {
	var events []Progress
	engine := &Engine{Observe: func(p Progress) { events = append(events, p) }}
	first, second := Progress{TaskID: "first"}, Progress{TaskID: "second"}
	for _, p := range []*Progress{&first, &second} {
		engine.emit(p, "start", "queued")
		engine.emit(p, "update", "downloading")
		p.Current = 1
		engine.emit(p, "update", "downloading")
		engine.lastUpdate[p.TaskID] = time.Now().Add(-time.Hour)
	}
	before := len(events)
	for _, p := range []*Progress{&first, &second} {
		p.Current = 2
		engine.emit(p, "update", "downloading")
	}
	if len(events) != before+2 || events[before].TaskID != "first" || events[before+1].TaskID != "second" {
		t.Fatal("An update from one task suppressed an eligible update from another task.", events)
	}
	for _, p := range []*Progress{&first, &second} {
		engine.lastUpdate[p.TaskID] = time.Now().Add(time.Hour)
		p.Current = 3
		engine.emit(p, "update", "downloading")
	}
	if len(events) != before+2 {
		t.Fatal("Repeated task updates bypassed throttling.", events)
	}
	engine.emit(&first, "retry", "retrying")
	engine.emit(&second, "done", "completed")
	engine.emit(&first, "cancelled", "cancelled")
	if len(events) != before+5 || len(engine.activeTasks) != 0 {
		t.Fatal("Boundary events were suppressed or completed tasks remain active.", events)
	}
	for i, event := range events {
		if event.Sequence != int64(i+1) {
			t.Fatal("Progress sequence numbers are not strictly increasing.", events)
		}
	}
}

func TestProgressIntervalScalesWithActiveTasks(t *testing.T) {
	updates := 0
	engine := &Engine{Observe: func(p Progress) {
		if p.State == "update" {
			updates++
		}
	}}
	for i := range 16 {
		p := Progress{TaskID: string(rune('a' + i))}
		engine.emit(&p, "start", "queued")
		p.Current = 1
		engine.emit(&p, "update", "downloading")
	}
	for taskID := range engine.activeTasks {
		engine.lastUpdate[taskID] = time.Now().Add(-200 * time.Millisecond)
	}
	for taskID := range engine.activeTasks {
		p := Progress{TaskID: taskID, Current: 2}
		engine.emit(&p, "update", "downloading")
	}
	if updates != 16 {
		t.Fatal("The update interval did not scale with active task counts.", updates)
	}
	for taskID := range engine.activeTasks {
		engine.lastUpdate[taskID] = time.Now().Add(-time.Hour)
		p := Progress{TaskID: taskID, Current: 3}
		engine.emit(&p, "update", "downloading")
	}
	if updates != 32 {
		t.Fatal("Eligible task updates were suppressed.", updates)
	}
}
