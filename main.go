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
	phases           *statemachine.MSM
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

func newPhases(m *MongoSync) *statemachine.MSM {
	return statemachine.NewMSM(
		Initiating,
		[]statemachine.Event{
			{Name: statemachine.NEXT, Src: Initiating, Dst: Initiated, Desc: "waiting for start command"},
			{Name: "start", Src: Initiated, Dst: CollectionAndIndexCreation,
				Desc: "Call fetchStartAtOperationTime to get initial cluster time, Call initializeCollections"},
			{Name: statemachine.NEXT, Src: CollectionAndIndexCreation, Dst: PartitionPrep, Desc: "Call initializeAllPartitions "},
			{Name: statemachine.NEXT, Src: PartitionPrep, Dst: CollectionDataCopy, Desc: "Call runCollectionCopy"},
			{Name: statemachine.NEXT, Src: CollectionDataCopy, Dst: ChangeStreamCapture, Desc: "Call CaptureChangeData"},
			{Name: statemachine.NEXT, Src: ChangeStreamCapture, Dst: CutOverDone, Desc: "Done"},
		},
		statemachine.Callbacks{
			Initiating: func(e *statemachine.Event) error {
				l.Println("==================================")
				l.Println("current state: ", m.state.Current())
				l.Println("current Phase: ", m.phases.Current())

				l.Println("loaded config data, done connecting source and destination")
				return nil
			},
			Initiated: func(e *statemachine.Event) error {
				l.Println("==================================")
				l.Println("current state: ", m.state.Current())
				l.Println("current Phase: ", m.phases.Current())
				m.resumePhase = m.phases.Current()
				return nil
			},
			CollectionAndIndexCreation: func(e *statemachine.Event) error {
				l.Println("==================================")
				l.Println("current state: ", m.state.Current())
				l.Println("current Phase: ", m.phases.Current())
				l.Println(" call ms.fetchStartAtOperationTime(ctx)")
				l.Println(" call ms.initializeCollections(ctx)")
				if err := processForOneSec(&m.ctx); err != nil {
					return err
				}
				m.resumePhase = m.phases.Current()
				return nil
			},
			PartitionPrep: func(e *statemachine.Event) error{
				l.Println("==================================")
				l.Println("current state: ", m.state.Current())
				l.Println("current Phase: ", m.phases.Current())
				l.Println("call ms.initializeAllPartitions(ctx)")
				if err := processForOneSec(&m.ctx); err != nil {
					m.state.Transit("stopped", false)
					return err
				}
				m.resumePhase = m.phases.Current()
				return nil
			},
			CollectionDataCopy: func(e *statemachine.Event) error {
				l.Println("==================================")
				l.Println("current state: ", m.state.Current())
				l.Println("current Phase: ", m.phases.Current())
				l.Println("call ms.runCollectionCopy(ctx)")
				m.resumePhase = m.phases.Current()
				if err := processForOneSec(&m.ctx); err != nil{
					m.state.Transit("stopped", false)
					return err
				}
				return nil
			},
			ChangeStreamCapture: func(e *statemachine.Event) error {
				l.Println("==================================")
				l.Println("current state: ", m.state.Current())
				l.Println("current Phase: ", m.phases.Current())
				l.Println("call ms.CaptureChangeData(ctx)")
				if err := processForOneSec(&m.ctx); err != nil {
					m.state.Transit("stopped", false)
					return err
				}
				m.resumePhase = m.phases.Current()
				for {
					if m.state.Current() == CuttingOver {
						time.Sleep(1 * time.Second)
						return nil
					}
					time.Sleep(100 * time.Millisecond)
				}
			},
			CutOverDone: func(event *statemachine.Event) error{
				l.Println("==================================")
				l.Println("current state: ", m.state.Current())
				l.Println("current Phase: ", m.phases.Current())
				m.resumePhase = m.phases.Current()
				m.state.Transit("cutOverDone", false)
				return nil
			},
		},
	)
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
			{Name: "abort", Src: Paused, Dst: Aborting},
			{Name: "abort", Src: Running,Dst: Aborting},
			{Name: "abort", Src: CuttingOver, Dst: Aborting},
			{Name: "stopped", Src: Aborting, Dst: Aborted},
		},
		statemachine.Callbacks{
			Running: func(e *statemachine.Event) error{
				l.Println("current event", e.Name)

				if m.resumeState != "" {
					l.Println("found resume data, current phase ", m.resumePhase)
					m.phases.SetState(m.resumePhase, true)
				} else {
					l.Println("found no resume data")
					m.phases.Transit("start", true)
				}
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
					m.phases.SetState(m.resumePhase, true)
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
			Aborting: func(e *statemachine.Event) error {
				m.cancel()
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
	abort := false
	restart := true
	resumePhase := PartitionPrep

	m.phases = newPhases(m)
	m.state = newState(m)

	fmt.Println(m.phases.GenerateDiagram())
	fmt.Println(m.state.GenerateDiagram())

	if err:= m.phases.SetState(Initiating, false); err != nil {
		fmt.Println(err)
	}

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

	if abort {
		time.Sleep(2500 * time.Millisecond)
		fmt.Println("sent abort")
		m.state.Transit("abort", true)
	}

	l.Println("send resume command")
	err := m.state.Transit("resume", true)
	if err != nil {
		l.Println(err.Error())
	}
	l.Println("done resume command")

	time.Sleep(10* time.Second)
	m.state.Transit("cutOver", true)
	time.Sleep(3 * time.Second)
	l.Println("final state: ", m.state.Current())
	l.Println("final phase: ", m.phases.Current())
}