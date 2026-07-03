package android

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtractLayout(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:layout_width="match_parent"
    android:layout_height="match_parent">
    <Button
        android:id="@+id/submitBtn"
        android:layout_width="wrap_content"
        android:layout_height="wrap_content" />
    <TextView
        android:layout_width="wrap_content"
        android:layout_height="wrap_content" />
    <com.example.widget.CircleImageView
        android:id="@+id/avatar"
        android:layout_width="48dp"
        android:layout_height="48dp" />
    <include layout="@layout/toolbar" />
</LinearLayout>`)

	result := &model.ParseResult{FilePath: "res/layout/activity_main.xml", Language: "xml"}
	ExtractLayout(content, result.FilePath, result)

	// Layout + 2 widgets (submitBtn, avatar) — TextView has no id so skipped
	if len(result.Symbols) != 3 {
		t.Fatalf("expected 3 symbols (1 layout + 2 widgets), got %d", len(result.Symbols))
	}

	// Layout symbol
	if result.Symbols[0].Kind != "Layout" || result.Symbols[0].Name != "activity_main" {
		t.Errorf("expected Layout 'activity_main', got %s '%s'", result.Symbols[0].Kind, result.Symbols[0].Name)
	}

	// Widget: submitBtn
	if result.Symbols[1].Name != "submitBtn" || result.Symbols[1].ClassType != "Button" {
		t.Errorf("expected widget 'submitBtn' type 'Button', got '%s' type '%s'", result.Symbols[1].Name, result.Symbols[1].ClassType)
	}

	// Widget: avatar (custom view)
	if result.Symbols[2].Name != "avatar" || result.Symbols[2].ClassType != "com.example.widget.CircleImageView" {
		t.Errorf("expected widget 'avatar' type 'com.example.widget.CircleImageView', got '%s' type '%s'", result.Symbols[2].Name, result.Symbols[2].ClassType)
	}

	// Edges: 2 CONTAINS + 1 INCLUDES + 1 REFERENCES (custom view)
	if len(result.Edges) != 4 {
		t.Fatalf("expected 4 edges, got %d", len(result.Edges))
	}

	includesCount := 0
	containsCount := 0
	referencesCount := 0
	for _, edge := range result.Edges {
		switch edge.Kind {
		case model.RelIncludes:
			includesCount++
		case model.RelContains:
			containsCount++
		case model.RelReferences:
			referencesCount++
		}
	}
	if includesCount != 1 {
		t.Errorf("expected 1 INCLUDES edge, got %d", includesCount)
	}
	if containsCount != 2 {
		t.Errorf("expected 2 CONTAINS edges, got %d", containsCount)
	}
	if referencesCount != 1 {
		t.Errorf("expected 1 REFERENCES edge, got %d", referencesCount)
	}
}

func TestExtractLayoutMerge(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<merge xmlns:android="http://schemas.android.com/apk/res/android">
    <ImageView android:id="@+id/icon" android:layout_width="24dp" android:layout_height="24dp" />
    <TextView android:id="@+id/label" android:layout_width="wrap_content" android:layout_height="wrap_content" />
</merge>`)

	result := &model.ParseResult{FilePath: "res/layout/view_merge.xml", Language: "xml"}
	ExtractLayout(content, result.FilePath, result)

	// 1 Layout + 2 widgets
	if len(result.Symbols) != 3 {
		t.Fatalf("expected 3 symbols, got %d", len(result.Symbols))
	}
	if result.Symbols[1].Name != "icon" {
		t.Errorf("expected 'icon', got '%s'", result.Symbols[1].Name)
	}
}

func TestExtractLayoutFragment(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FrameLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:layout_width="match_parent"
    android:layout_height="match_parent">
    <fragment
        android:id="@+id/mapFragment"
        android:name="com.example.app.MapFragment"
        android:layout_width="match_parent"
        android:layout_height="match_parent" />
</FrameLayout>`)

	result := &model.ParseResult{FilePath: "res/layout/fragment_map.xml", Language: "xml"}
	ExtractLayout(content, result.FilePath, result)

	// 1 Layout + 1 Widget (mapFragment with ClassType = class name)
	if len(result.Symbols) != 2 {
		t.Fatalf("expected 2 symbols (Layout + Widget), got %d", len(result.Symbols))
	}

	widget := result.Symbols[1]
	if widget.Name != "mapFragment" {
		t.Errorf("expected widget name 'mapFragment', got '%s'", widget.Name)
	}
	if widget.ClassType != "com.example.app.MapFragment" {
		t.Errorf("expected ClassType 'com.example.app.MapFragment', got '%s'", widget.ClassType)
	}

	// Should have REFERENCES edge (Widget → Class)
	foundReference := false
	for _, edge := range result.Edges {
		if edge.Kind == model.RelReferences && edge.TargetID == "com.example.app.MapFragment" {
			foundReference = true
		}
	}
	if !foundReference {
		t.Error("expected REFERENCES edge for com.example.app.MapFragment")
	}
}
