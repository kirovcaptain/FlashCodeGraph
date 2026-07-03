package android

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtractNavigation(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<navigation xmlns:android="http://schemas.android.com/apk/res/android"
    xmlns:app="http://schemas.android.com/apk/res-auto"
    android:id="@+id/nav_graph"
    app:startDestination="@id/homeFragment">
    <fragment
        android:id="@+id/homeFragment"
        android:name="com.example.HomeFragment"
        android:label="Home">
        <action
            android:id="@+id/action_home_to_detail"
            app:destination="@id/detailFragment" />
    </fragment>
    <fragment
        android:id="@+id/detailFragment"
        android:name="com.example.DetailFragment"
        android:label="Detail">
        <deepLink app:uri="https://example.com/detail/{id}" />
    </fragment>
</navigation>`)

	result := &model.ParseResult{FilePath: "res/navigation/nav_graph.xml", Language: "xml"}
	ExtractNavigation(content, result.FilePath, result)

	// 2 NAVIGATE + 1 ACTION + 1 DEEP_LINK + 1 START_DESTINATION = 5
	if len(result.Routes) != 5 {
		t.Fatalf("expected 5 routes, got %d", len(result.Routes))
	}

	// First destination
	if result.Routes[0].Method != "NAVIGATE" || result.Routes[0].PathPattern != "Home" {
		t.Errorf("expected NAVIGATE 'Home', got %s '%s'", result.Routes[0].Method, result.Routes[0].PathPattern)
	}

	// Action
	if result.Routes[1].Method != "ACTION" || result.Routes[1].PathPattern != "action_home_to_detail" {
		t.Errorf("expected ACTION 'action_home_to_detail', got %s '%s'", result.Routes[1].Method, result.Routes[1].PathPattern)
	}

	// Deep link
	found := false
	for _, route := range result.Routes {
		if route.Method == "DEEP_LINK" && route.PathPattern == "https://example.com/detail/{id}" {
			found = true
		}
	}
	if !found {
		t.Error("expected DEEP_LINK route with uri")
	}

	// Start destination
	foundStart := false
	for _, route := range result.Routes {
		if route.Method == "START_DESTINATION" && route.PathPattern == "homeFragment" {
			foundStart = true
		}
	}
	if !foundStart {
		t.Error("expected START_DESTINATION for homeFragment")
	}
}
