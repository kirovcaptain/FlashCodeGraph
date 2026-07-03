package android

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func TestExtractManifest(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.app">
    <application>
        <activity android:name=".MainActivity" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity>
        <activity android:name=".DetailActivity" />
        <service android:name=".sync.SyncService" />
        <receiver android:name="com.example.other.Receiver" />
    </application>
</manifest>`)

	result := &model.ParseResult{FilePath: "app/src/main/AndroidManifest.xml", Language: "xml"}
	ExtractManifest(content, result.FilePath, "", result)

	if len(result.Symbols) != 4 {
		t.Fatalf("expected 4 AppComponent symbols, got %d", len(result.Symbols))
	}

	// Launcher activity
	if result.Symbols[0].QualifiedName != "com.example.app.MainActivity" {
		t.Errorf("expected com.example.app.MainActivity, got %s", result.Symbols[0].QualifiedName)
	}
	if result.Symbols[0].Metadata["is_launcher"] != "true" {
		t.Error("expected MainActivity to be marked as launcher (Metadata[is_launcher]=true)")
	}

	// Regular activity
	if result.Symbols[1].QualifiedName != "com.example.app.DetailActivity" {
		t.Errorf("expected com.example.app.DetailActivity, got %s", result.Symbols[1].QualifiedName)
	}

	// Service with nested package
	if result.Symbols[2].QualifiedName != "com.example.app.sync.SyncService" {
		t.Errorf("expected com.example.app.sync.SyncService, got %s", result.Symbols[2].QualifiedName)
	}
	if result.Symbols[2].ClassType != "service" {
		t.Errorf("expected component_type=service, got %s", result.Symbols[2].ClassType)
	}

	// Fully qualified receiver
	if result.Symbols[3].QualifiedName != "com.example.other.Receiver" {
		t.Errorf("expected com.example.other.Receiver, got %s", result.Symbols[3].QualifiedName)
	}

	// REFERENCES edges
	if len(result.Edges) != 4 {
		t.Fatalf("expected 4 REFERENCES edges, got %d", len(result.Edges))
	}
}

func TestExtractManifestWithModulePackage(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application>
        <activity android:name=".MainActivity" />
    </application>
</manifest>`)

	result := &model.ParseResult{FilePath: "AndroidManifest.xml", Language: "xml"}
	ExtractManifest(content, result.FilePath, "com.google.samples.apps.nowinandroid", result)

	if len(result.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(result.Symbols))
	}
	if result.Symbols[0].QualifiedName != "com.google.samples.apps.nowinandroid.MainActivity" {
		t.Errorf("expected full qualified name with modulePackage, got %s", result.Symbols[0].QualifiedName)
	}
}

func TestExtractManifestDeepLink(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.app">
    <application>
        <activity android:name=".MainActivity">
            <intent-filter>
                <action android:name="android.intent.action.VIEW" />
                <data android:scheme="https" android:host="example.com" />
            </intent-filter>
        </activity>
    </application>
</manifest>`)

	result := &model.ParseResult{FilePath: "AndroidManifest.xml", Language: "xml"}
	ExtractManifest(content, result.FilePath, "", result)

	if len(result.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(result.Symbols))
	}

	// Deep link stored in Symbol Metadata
	deepLinks := result.Symbols[0].Metadata["deep_links"]
	if deepLinks != "https://example.com" {
		t.Errorf("expected deep_links=https://example.com in Metadata, got %s", deepLinks)
	}

	// REFERENCES edge should only have ref_kind
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(result.Edges))
	}
	refKind, ok := result.Edges[0].Properties["ref_kind"].(string)
	if !ok || refKind != "manifest" {
		t.Errorf("expected ref_kind=manifest, got %v", result.Edges[0].Properties["ref_kind"])
	}
}
