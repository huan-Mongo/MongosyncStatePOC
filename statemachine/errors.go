// Copyright (c) 2013 - Max Persson <max@looplab.se>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package statemachine

// InvalidEventError is returned by MSM.Transit() when the event cannot be called
// in the current state.
type InvalidEventError struct {
	Event string
	State string
}

func (e InvalidEventError) Error() string {
	return "event " + e.Event + " inappropriate in current state " + e.State
}

// UnknownEventError is returned by MSM.Transit() when the event is not defined.
type UnknownEventError struct {
	Event string
}

func (e UnknownEventError) Error() string {
	return "event " + e.Event + " does not exist"
}

// InTransitionError is returned by MSM.Transit() when an asynchronous transition
// is already in progress.
type InTransitionError struct {
	Event string
}

func (e InTransitionError) Error() string {
	return "event " + e.Event + " inappropriate because previous transition did not complete"
}

