package plan

import (
	"errors"
	"testing"
)

// 测试辅助：指针构造器。
func ptrStatus(s StepStatus) *StepStatus { return &s }
func ptrStr(v string) *string           { return &v }

func TestCreateAndGet(t *testing.T) {
	s := NewStore(0)
	p, err := s.Create("sess:1", "完成任务X", []StepSpec{
		{Title: "步骤一"},
		{Title: "步骤二", Detail: "细节"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if p.ID == "" || p.Title != "完成任务X" {
		t.Fatalf("bad plan: %+v", p)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(p.Steps))
	}
	if p.Steps[0].ID != "s1" || p.Steps[1].ID != "s2" {
		t.Fatalf("step ids wrong: %v", p.Steps)
	}
	got, err := s.Get("sess:1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Title != p.Title || len(got.Steps) != 2 {
		t.Fatalf("Get mismatch")
	}
	if !s.Has("sess:1") {
		t.Fatalf("Has should be true")
	}
	if !s.IsOpen("sess:1") {
		t.Fatalf("plan should be open")
	}
}

func TestCreateErrors(t *testing.T) {
	s := NewStore(0)
	if _, err := s.Create("sess", "", nil); !errors.Is(err, ErrEmptyTitle) {
		t.Fatalf("expected ErrEmptyTitle, got %v", err)
	}
	if _, err := s.Create("sess", "  ", []StepSpec{{Title: "x"}}); !errors.Is(err, ErrEmptyTitle) {
		t.Fatalf("expected ErrEmptyTitle for whitespace, got %v", err)
	}
	if _, err := s.Create("sess", "t", nil); !errors.Is(err, ErrNoSteps) {
		t.Fatalf("expected ErrNoSteps, got %v", err)
	}
	if _, err := s.Create("sess", "t", []StepSpec{{Title: " "}}); !errors.Is(err, ErrNoSteps) {
		t.Fatalf("expected ErrNoSteps for empty step titles, got %v", err)
	}
}

func TestUpdateStepTransitions(t *testing.T) {
	s := NewStore(0)
	_, _ = s.Create("sess", "t", []StepSpec{{Title: "a"}, {Title: "b"}})

	// in_progress
	p, err := s.UpdateStep("sess", "s1", StepPatch{Status: ptrStatus(StepInProgress)})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if p.Steps[0].Status != StepInProgress {
		t.Fatalf("status not in_progress")
	}
	if p.Revision != 2 {
		t.Fatalf("revision should bump to 2, got %d", p.Revision)
	}

	// done with note
	p, err = s.UpdateStep("sess", "1", StepPatch{Status: ptrStatus(StepDone), Note: ptrStr("done a")})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if p.Steps[0].Status != StepDone || p.Steps[0].Note != "done a" {
		t.Fatalf("done update wrong: %+v", p.Steps[0])
	}

	// skipped requires note
	if _, err := s.UpdateStep("sess", "s2", StepPatch{Status: ptrStatus(StepSkipped)}); !errors.Is(err, ErrTerminalNoNote) {
		t.Fatalf("expected ErrTerminalNoNote, got %v", err)
	}
	p, err = s.UpdateStep("sess", "s2", StepPatch{Status: ptrStatus(StepSkipped), Note: ptrStr("n/a")})
	if err != nil {
		t.Fatalf("skipped update failed: %v", err)
	}
	if p.IsOpen() {
		t.Fatalf("plan should be closed now (both steps terminal): %s", p.Render())
	}
	if s.IsOpen("sess") {
		t.Fatalf("store IsOpen should be false once all steps terminal")
	}
}

func TestUpdateStepNotFound(t *testing.T) {
	s := NewStore(0)
	s.Create("sess", "t", []StepSpec{{Title: "a"}})
	if _, err := s.UpdateStep("sess", "s9", StepPatch{Status: ptrStatus(StepDone)}); !errors.Is(err, ErrStepNotFound) {
		t.Fatalf("expected ErrStepNotFound, got %v", err)
	}
	if _, err := s.UpdateStep("nope", "s1", StepPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAddSteps(t *testing.T) {
	s := NewStore(0)
	p, _ := s.Create("sess", "t", []StepSpec{{Title: "a"}})
	p, err := s.AddSteps("sess", []StepSpec{{Title: "b"}, {Title: "c"}})
	if err != nil {
		t.Fatalf("AddSteps failed: %v", err)
	}
	if len(p.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(p.Steps))
	}
	if p.Steps[1].ID != "s2" || p.Steps[2].ID != "s3" {
		t.Fatalf("ids wrong: %v", p.Steps)
	}
	if p.Revision != 2 {
		t.Fatalf("revision should be 2, got %d", p.Revision)
	}
}

func TestCountsNextFindRender(t *testing.T) {
	s := NewStore(0)
	p, _ := s.Create("sess", "t", []StepSpec{{Title: "a"}, {Title: "b"}, {Title: "c"}})
	p, _ = s.UpdateStep("sess", "s1", StepPatch{Status: ptrStatus(StepDone), Note: ptrStr("x")})
	c := p.Counts()
	if c.Total != 3 || c.Done != 1 || c.Pending != 2 || c.Open() != 2 {
		t.Fatalf("counts wrong: %+v", c)
	}
	next := p.Next()
	if next == nil || next.ID != "s2" {
		t.Fatalf("Next should be s2, got %+v", next)
	}
	for _, id := range []string{"s2", "S2", "2"} {
		if st, ok := p.Find(id); !ok || st.ID != "s2" {
			t.Fatalf("Find(%q) failed: %+v %v", id, st, ok)
		}
	}
	if r := p.Render(); r == "" {
		t.Fatalf("Render empty")
	}
	if pr := p.RenderProgress(); pr == "" {
		t.Fatalf("RenderProgress empty")
	}
}

func TestNormalizeStepID(t *testing.T) {
	cases := map[string]string{
		"1":   "s1",
		"S2":  "s2",
		" s3 ": "s3",
		"":    "",
		"abc": "abc",
	}
	for in, want := range cases {
		if got := NormalizeStepID(in); got != want {
			t.Fatalf("NormalizeStepID(%q)=%q want %q", in, got, want)
		}
	}
}

func TestStoreEviction(t *testing.T) {
	s := NewStore(2)
	for i := 0; i < 5; i++ {
		ch := rune('a' + i)
		s.Create("sess:"+string(ch), "t", []StepSpec{{Title: "x"}})
	}
	if s.Len() > 2 {
		t.Fatalf("expected <=2 scopes after eviction, got %d", s.Len())
	}
}

func TestCreateOverwrites(t *testing.T) {
	s := NewStore(0)
	s.Create("sess", "t1", []StepSpec{{Title: "a"}})
	p2, _ := s.Create("sess", "t2", []StepSpec{{Title: "b"}, {Title: "c"}})
	if len(p2.Steps) != 2 || p2.Title != "t2" {
		t.Fatalf("Create should overwrite: %+v", p2)
	}
}

func TestDelete(t *testing.T) {
	s := NewStore(0)
	s.Create("sess", "t", []StepSpec{{Title: "a"}})
	if !s.Has("sess") {
		t.Fatalf("should have plan before delete")
	}
	s.Delete("sess")
	if s.Has("sess") {
		t.Fatalf("should not have plan after delete")
	}
}
