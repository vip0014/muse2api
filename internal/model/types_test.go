package model

import "testing"

func TestTaskTransitions(t *testing.T) {
	valid := [][2]TaskStatus{{TaskQueued, TaskRunning}, {TaskRunning, TaskSucceeded}, {TaskRunning, TaskFailed}, {TaskQueued, TaskCancelled}}
	for _, pair := range valid {
		if !ValidTaskTransition(pair[0], pair[1]) {
			t.Fatalf("rejected %v", pair)
		}
	}
	if ValidTaskTransition(TaskSucceeded, TaskRunning) || ValidTaskTransition(TaskFailed, TaskQueued) {
		t.Fatal("terminal task reopened")
	}
}
