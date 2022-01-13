package main

import (
	"./statemachine"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"
)

type MongoSync struct {
	state            *statemachine.MSM
	ctx              context.Context
	cancel           context.CancelFunc
	resumePhase      string
	resumeState      string
}

var l *log.Logger

const  (
	NA = "N/A"
	Initiating = "Initiating"
	Initiated = "Initiated"
	CollectionAndIndexCreation = "CollectionAndIndexCreation"
	PartitionPrep = "PartitionPrep"
	CollectionDataCopy = "CollectionDataCopy"
	ChangeStreamCapture = "ChangeStreamCapture"
	CutOverDone = "CutOverDone"
)

const  (
	Idle = "Idle"
	Running = "Running"
	Pausing = "Pausing"
	Paused = "Paused"
	Resuming = "Resuming"
	Aborting = "Aborting"
	Aborted = "Aborted"
	CuttingOver = "CuttingOver"
	CutOverCompleted = "CutOverCompleted"
)

// return error when context is canceled
func processForOneSec(ctx *context.Context) error {
	localCtx, cancel := context.WithTimeout(*ctx, 1 * time.Second)
	defer cancel()
	for{
		select {
		case <- localCtx.Done():
			if errors.Is(localCtx.Err(), context.Canceled) {
				fmt.Println(localCtx.Err().Error())
				return localCtx.Err()
			} else {
				return nil
			}
		}
	}
}

// This logic is similar to what's inside Mongosync now
func runReplication(m *MongoSync) {
	switch m.resumePhase {
	case Initiated:
		l.Println("==================================")
		l.Println("current state: ", m.state.Current())
		l.Println("current Phase: ", m.resumePhase)
		m.resumePhase = CollectionAndIndexCreation
		fallthrough
	case CollectionAndIndexCreation:
		l.Println("==================================")
		l.Println("current state: ", m.state.Current())
		l.Println("current Phase: ", m.resumePhase)
		l.Println(" call ms.fetchStartAtOperationTime(ctx)")
		l.Println(" call ms.initializeCollections(ctx)")
		if err := processForOneSec(&m.ctx); err != nil {
			m.state.Transit("stopped", false)
		}
		m.resumePhase = PartitionPrep
		fallthrough
	case PartitionPrep:
		l.Println("==================================")
		l.Println("current state: ", m.state.Current())
		l.Println("current Phase: ", m.resumePhase)
		l.Println("call ms.initializeAllPartitions(ctx)")
		if err := processForOneSec(&m.ctx); err != nil {
			m.state.Transit("stopped", false)
		}
		m.resumePhase = CollectionDataCopy
		fallthrough
	case CollectionDataCopy:
		l.Println("==================================")
		l.Println("current state: ", m.state.Current())
		l.Println("current Phase: ", m.resumePhase)
		l.Println("call ms.runCollectionCopy(ctx)")
		m.resumePhase = m.resumePhase
		if err := processForOneSec(&m.ctx); err != nil{
			m.state.Transit("stopped", false)
		}
		m.resumePhase = ChangeStreamCapture
		fallthrough
	case ChangeStreamCapture:
		l.Println("==================================")
		l.Println("current state: ", m.state.Current())
		l.Println("current Phase: ", m.resumePhase)
		l.Println("call ms.CaptureChangeData(ctx)")
		if err := processForOneSec(&m.ctx); err != nil {
			m.state.Transit("stopped", false)
		}
		fmt.Println("check cutover")
		for {
			if m.state.Current() == CuttingOver {
				time.Sleep(1 * time.Second)
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		m.resumePhase = CutOverDone
		fallthrough
	case CutOverDone:
		l.Println("==================================")
		l.Println("current state: ", m.state.Current())
		l.Println("current Phase: ", m.resumePhase)
		m.state.Transit("cutOverDone", false)
	}
}

func newState(m *MongoSync) *statemachine.MSM {
	return statemachine.NewMSM(
		Idle,
		[]statemachine.Event{
			{Name: "start", Src: Idle, Dst: Running},
			{Name: "pause", Src: Running, Dst: Pausing},
			{Name: "stopped", Src: Pausing, Dst: Paused},
			{Name: "resume", Src: Paused, Dst: Resuming},
			{Name: statemachine.NEXT, Src: Resuming, Dst: Running},
			{Name: "cutOver", Src: Running, Dst: CuttingOver},
			{Name: "cutOverDone", Src: CuttingOver, Dst: CutOverCompleted},
			{Name: "stopped", Src: Aborting, Dst: Aborted},
		},
		statemachine.Callbacks{
			Running: func(e *statemachine.Event) error{
				l.Println("current event", e.Name)
				go func() {
					runReplication(m)
				}()
				return nil
			},
			Pausing: func(e *statemachine.Event) error {
				m.resumeState = e.Src
				m.cancel()
				return nil
			},
			CuttingOver: func(e *statemachine.Event) error {
				l.Println("current event", e.Name)
				l.Println("current state", m.state.Current())

				if e.Name == "_set_state" {
					go func(){
						runReplication(m)
					}()
				}
				return nil
			},
			Resuming: func(e *statemachine.Event) error {
				l.Println("current event", e.Name)
				l.Println("current state", m.state.Current())

				l.Printf("loaded resume data, phase before pause %s, done connecting source and destination",
					m.resumePhase)
				m.ctx, m.cancel= context.WithCancel(context.Background())
				return nil
			},
		},
	)
}

func main() {
	l = log.New(os.Stdout, "", 2)

	m := &MongoSync{
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())

	pause := true
	restart := true
	resumePhase := PartitionPrep

	m.resumePhase = Initiated
	m.state = newState(m)

	fmt.Println(m.state.GenerateDiagram())

	l.Println("==================================")
	l.Println("current state: ", m.state.Current())
	l.Println("current Phase: ", m.resumePhase)

	l.Println("loaded config data, done connecting source and destination")
	m.resumePhase = Initiated

	if !restart {
		err := m.state.Transit("start", true)
		l.Println("done start command")
		if err != nil {
			fmt.Println(err)
		}
	} else{
		m.resumePhase = resumePhase
		m.resumeState = Running
		l.Printf("recovered, resuming state: %s, phase: %s", m.resumeState, m.resumePhase)
		err := m.state.SetState(m.resumeState, true)
		if err != nil {
			fmt.Println(err)
		}
	}

	if pause {
		time.Sleep(2500 * time.Millisecond)
		fmt.Println("sent pause")
		m.state.Transit("pause", false)

		for {
			if m.state.Current() == Paused{
				break
			}
		}
	}

	l.Println("send resume command")
	err := m.state.Transit("resume", true)
	if err != nil {
		l.Println(err.Error())
	}
	l.Println("done resume command")

	time.Sleep(10* time.Second)
	l.Println("send cutover")
	m.state.Transit("cutOver", true)
	time.Sleep(3 * time.Second)
	l.Println("final state: ", m.state.Current())
	l.Println("final phase: ", m.resumePhase)
}