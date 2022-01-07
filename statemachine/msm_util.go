package statemachine

import (
	"bytes"
	"fmt"
	"sync"
)

type StateInfo struct {
	// eventName -> dst
	events map[string]string
	desc string
}

func newStateInfo() StateInfo{
	return StateInfo{
		events: make(map[string]string),
	}
}

const NEXT = "_next"

type MSM struct {
	// current is the state that the MSM is currently in.
	current string

	states map[string]StateInfo

	// callbacks maps events and targets to callback functions.
	callbacks map[string]Callback

	inTransition bool

	// stateMu guards access to the current state.
	stateMu sync.RWMutex
	// eventMu guards access to Transit()
	eventMu sync.Mutex
}

type Event struct {
	Name string
	Src string
	Dst string
	Desc string
}

// Callback is a function type that callbacks should use. Event is the current
// event info as the callback happens.
type Callback func(*Event) error

// Callbacks is a shorthand for defining the callbacks in NewMSM.
type Callbacks map[string]Callback

func NewMSM(initial string, events []Event, callbacks map[string]Callback) *MSM {
	m := &MSM{
		current:         initial,
		states:          make(map[string]StateInfo),
		callbacks:       make(map[string]Callback),
	}

	// Build transition map and store sets of all events and states.
	for _, e := range events {
		if _, ok := m.states[e.Src]; !ok {
			m.states[e.Src] = newStateInfo()
		}
		m.states[e.Src].events[e.Name] = e.Dst

		if _, ok := m.states[e.Dst]; !ok {
			m.states[e.Dst] = newStateInfo()
		}

		if e.Desc != "" {
			m.states[e.Dst] = StateInfo{
				events: m.states[e.Dst].events,
				desc:   e.Desc,
			}
		}
	}

	// Map all callbacks to events/states.
	for target, fn := range callbacks {
		m.callbacks[target] = fn
	}

	return m
}

// Current returns the current state of the MSM.
func (m *MSM) Current() string {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.current
}

// SetState allows the user to move to the given state from current state and
// trigger any callbacks, if defined.
func (m *MSM) SetState(state string, async bool) error {
	return m.transition(state, "_set_state", async)
}

// Can returns true if event can occur in the current state.
func (m *MSM) Can(event string) bool {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	v, ok := m.states[m.current]
	if ok {
		_, ok = v.events[event]
	}
	return ok && !m.inTransition
}


func (m *MSM) transition(dst string, eventName string, async bool) error {
	if m.inTransition {
		return InTransitionError{eventName}
	}

	m.eventMu.Lock()
	var unlocked bool
	defer func() {
		if !unlocked && !async {
			m.eventMu.Unlock()
		}
	}()

	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	e := &Event{eventName, m.current, dst, ""}

	m.stateMu.RUnlock()
	defer m.stateMu.RLock()

	m.inTransition = true

	// Setup the transition, call it later.
	m.stateMu.Lock()
	m.current = dst
	m.stateMu.Unlock()

	if fn, ok := m.callbacks[m.current]; ok {
		if !async {
			if err := fn(e); err != nil {
				// exit if the current callback returns error. Won't proceed the other implicit transitions.
				m.inTransition = false
				return err
			}

			m.eventMu.Unlock()
			unlocked = true
			m.inTransition = false

			if m.Can(NEXT) {
				m.transit(NEXT, false)
			}
		} else {
			go func() error {
				err := fn(e)
				// exit if the current callback returns error. Won't proceed the other implicit transitions.
				m.eventMu.Unlock()
				unlocked = true
				m.inTransition = false

				if err == nil {
					if m.Can(NEXT) {
						m.transit(NEXT, false)
					}
				}

				return nil
			}()
		}
	} else {
		m.eventMu.Unlock()
		unlocked = true
		m.inTransition = false
	}

	return nil
}

func (m *MSM) transit(eventName string, async bool) error {
	var dst string
	v, ok := m.states[m.current]
	if ok {
		dst, ok = v.events[eventName]
	}

	if !ok {
		return InvalidEventError{eventName, m.current}
	}

	return m.transition(dst, eventName, async)
}

func (m *MSM) Transit(eventName string, async bool) error {
	if eventName == NEXT {
		return InvalidEventError{eventName, m.current}
	}

	return m.transit(eventName, async)
}

func (m *MSM) GenerateDiagram()  string {
	var buf bytes.Buffer

	buf.WriteString("@startuml\n")
	buf.WriteString(fmt.Sprintln(`    [*] -->`, m.current))

	withDesc := false
	for src, stateInfo := range m.states{
		for event, dst := range stateInfo.events {
			buf.WriteString(fmt.Sprintf(`    %s --> %s: %s`, src, dst, event))
			buf.WriteString("\n")
		}

		if len(stateInfo.events) == 0 {
			buf.WriteString(fmt.Sprintf(`    %s --> [*]`, src))
			buf.WriteString("\n")
		}
		if stateInfo.desc != "" {
			withDesc = true
			buf.WriteString(fmt.Sprintf(`    %s: %s`, src, stateInfo.desc))
			buf.WriteString("\n")
		}
	}

	if !withDesc {
		buf.WriteString("hide empty description\n")
	}

	buf.WriteString("@enduml\n")

	return buf.String()
}
