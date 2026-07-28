package repo

import (
	"testing"

	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newTestDB opens an in-memory SQLite database and migrates the model tables.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // keep a single connection for :memory:
	if err := db.AutoMigrate(&model.Provider{}, &model.Model{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestUpsertModelPreservesFlags(t *testing.T) {
	db := newTestDB(t)

	// First upsert: created with Enabled=false.
	m := &model.Model{ProviderID: 1, UserID: 7, ModelID: "gpt-4o", Name: "GPT-4o", Enabled: false}
	if err := UpsertModel(db, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if m.ID == 0 {
		t.Fatalf("expected row id to be assigned")
	}

	// Enable it directly and re-upsert with the same provider/model id.
	enabled := true
	if err := PatchModel(db, m, &enabled, nil); err != nil {
		t.Fatalf("patch: %v", err)
	}

	// Re-sync (simulating a discover refresh) must NOT reset Enabled back to false.
	refreshed := &model.Model{ProviderID: 1, UserID: 7, ModelID: "gpt-4o", Name: "GPT-4o (renamed)"}
	if err := UpsertModel(db, refreshed); err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	if !refreshed.Enabled {
		t.Fatalf("upsert reset user's Enabled flag; expected true")
	}
	if refreshed.Name != "GPT-4o (renamed)" {
		t.Fatalf("upsert did not refresh Name; got %q", refreshed.Name)
	}

	// Exactly one row should exist for this provider/model pair.
	list, err := ListModelsByProvider(db, 1, 7)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 managed model after upsert, got %d", len(list))
	}
}

func TestPatchModelSingleDefaultPerProvider(t *testing.T) {
	db := newTestDB(t)

	a := &model.Model{ProviderID: 1, UserID: 7, ModelID: "gpt-4o", Name: "GPT-4o"}
	b := &model.Model{ProviderID: 1, UserID: 7, ModelID: "gpt-4o-mini", Name: "GPT-4o-mini"}
	if err := UpsertModel(db, a); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := UpsertModel(db, b); err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	def := true
	if err := PatchModel(db, a, &def, &def); err != nil {
		t.Fatalf("set a default: %v", err)
	}
	// Now mark b as default; a must be cleared.
	if err := PatchModel(db, b, &def, &def); err != nil {
		t.Fatalf("set b default: %v", err)
	}

	var reload []model.Model
	db.Where("user_id = ?", 7).Find(&reload)
	var defaults int
	for _, m := range reload {
		if m.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly 1 default, got %d", defaults)
	}
	if !b.IsDefault {
		t.Fatalf("expected b to be the default after the second patch")
	}
}

func TestListEnabledModelsScopedToUser(t *testing.T) {
	db := newTestDB(t)

	en := true
	dis := false
	m1 := &model.Model{ProviderID: 1, UserID: 7, ModelID: "gpt-4o", Enabled: true}
	m2 := &model.Model{ProviderID: 1, UserID: 7, ModelID: "gpt-4o-mini", Enabled: false}
	m3 := &model.Model{ProviderID: 1, UserID: 9, ModelID: "other", Enabled: true}
	if err := UpsertModel(db, m1); err != nil {
		t.Fatalf("upsert m1: %v", err)
	}
	if err := UpsertModel(db, m2); err != nil {
		t.Fatalf("upsert m2: %v", err)
	}
	if err := UpsertModel(db, m3); err != nil {
		t.Fatalf("upsert m3: %v", err)
	}
	_ = en
	_ = dis

	enabled, err := ListEnabledModels(db, 7)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ModelID != "gpt-4o" {
		t.Fatalf("expected only user 7's enabled model, got %+v", enabled)
	}
}
