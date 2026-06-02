#!/usr/bin/env python3
"""
check_debug_data.py — Detect duplicate IDs, orphan edges, and type mismatches in FCG debug dump.

Usage:
    python3 analysis/check_debug_data.py /path/to/project
"""

import csv
import os
import sys
from collections import defaultdict

# Edge table schema: edge_type -> (from_kind, to_kind)
EDGE_SCHEMA = {
    "CALLS": ("Function", ("Function", "Class", "Interface")),
    "EXTENDS": ("Class", "Class"),
    "IMPLEMENTS": ("Class", "Interface"),
    "IMPORTS": ("File", "File"),
    "OVERRIDES": ("Function", "Function"),
    "DISPATCHES": ("Function", "Function"),
    "CONTAINS": None,  # Dynamic routing by SourceKind, skip type check
    "DIR_CONTAINS": ("Directory", "File"),
    "FILE_CONTAINS": ("File", "Function"),
    "FILE_CONTAINS_CLASS": ("File", "Class"),
    "FILE_CONTAINS_IFACE": ("File", "Interface"),
    "FILE_CONTAINS_VAR": ("File", "Variable"),
    "CLASS_CONTAINS_FUNC": ("Class", "Function"),
    "IFACE_CONTAINS_FUNC": ("Interface", "Function"),
    "CLASS_CONTAINS_VAR": ("Class", "Variable"),
    "HANDLES": ("Function", "Route"),
    "UNRESOLVED_CALL": ("Function", "Function"),
    "USES": ("Function", "Variable"),
}


def find_debug_dir(project_path):
    debug_dir = os.path.join(project_path, ".fcg", "debug")
    if not os.path.isdir(debug_dir):
        print(f"❌ Debug directory not found: {debug_dir}")
        print("   Run: fcg index --debug --force")
        sys.exit(1)
    return debug_dir


def collect_node_ids(debug_dir):
    """Read all node CSVs and collect id -> kind mapping. Returns (id_to_kind, duplicates)."""
    node_files = [
        ("symbols.csv", 0, 1),           # id col 0, kind col 1
        ("routes.csv", 0, None),         # id col 0, kind is always "Route"
        ("structural_nodes.csv", 0, 1),  # id col 0, kind col 1
        ("external_nodes.csv", 0, 1),   # id col 0, kind col 1
    ]

    # Fixed kinds for files without kind column
    fixed_kinds = {
        "routes.csv": "Route",
    }

    id_to_kind = {}  # id -> kind
    id_sources = defaultdict(list)  # id -> [(file, line)]
    duplicates = []

    for filename, id_column, kind_column in node_files:
        filepath = os.path.join(debug_dir, filename)
        if not os.path.exists(filepath):
            continue
        fixed_kind = fixed_kinds.get(filename)
        with open(filepath, "r", encoding="utf-8") as f:
            reader = csv.reader(f)
            header = next(reader, None)
            if not header:
                continue
            for line_number, row in enumerate(reader, start=2):
                if len(row) <= id_column:
                    continue
                node_id = row[id_column]
                kind = fixed_kind if fixed_kind else (row[kind_column] if kind_column and len(row) > kind_column else "Unknown")

                id_sources[node_id].append((filename, line_number))
                if node_id in id_to_kind:
                    continue  # already recorded, will check duplicates below
                id_to_kind[node_id] = kind

    for node_id, sources in id_sources.items():
        if len(sources) > 1:
            duplicates.append((node_id, id_to_kind.get(node_id, "?"), sources))

    return id_to_kind, duplicates


def check_edges(debug_dir, id_to_kind):
    """Read edge CSVs and check orphan edges + type mismatches."""
    edge_files = [
        ("structural_edges.csv", 0, 1, 2),  # source col 0, target col 1, kind col 2
        ("heritage.csv", 0, 1, 2),       # source col 0, target col 1, kind col 2
        ("overrides.csv", 0, 1, 2),      # source col 0, target col 1, kind col 2
        ("implements.csv", 0, 1, 2),     # source col 0, target col 1, kind col 2
    ]

    # calls_*.csv: resolved_by, confidence, candidates, source_id, target_id, ...
    for filename in os.listdir(debug_dir):
        if filename.startswith("calls_") and filename.endswith(".csv"):
            edge_files.append((filename, 3, 4, None))  # source col 3, target col 4, kind inferred

    orphans = []
    type_mismatches = []

    for filename, source_column, target_column, kind_column in edge_files:
        filepath = os.path.join(debug_dir, filename)
        if not os.path.exists(filepath):
            continue

        # Infer edge type from filename for calls_*.csv
        inferred_edge_type = None
        if filename.startswith("calls_"):
            inferred_edge_type = "CALLS"

        with open(filepath, "r", encoding="utf-8") as f:
            reader = csv.reader(f)
            header = next(reader, None)
            if not header:
                continue
            for line_number, row in enumerate(reader, start=2):
                if len(row) <= max(source_column, target_column):
                    continue
                source_id = row[source_column]
                target_id = row[target_column]
                edge_type = inferred_edge_type
                if kind_column is not None and len(row) > kind_column:
                    edge_type = row[kind_column]

                # Check orphans
                if source_id and source_id not in id_to_kind:
                    orphans.append((filename, line_number, "source", source_id))
                if target_id and target_id not in id_to_kind:
                    orphans.append((filename, line_number, "target", target_id))

                # Check type mismatch
                if edge_type and edge_type in EDGE_SCHEMA and EDGE_SCHEMA[edge_type] is not None:
                    expected_from_kind, expected_to_kind = EDGE_SCHEMA[edge_type]
                    source_kind = id_to_kind.get(source_id)
                    target_kind = id_to_kind.get(target_id)
                    from_ok = isinstance(expected_from_kind, tuple) and source_kind in expected_from_kind or source_kind == expected_from_kind
                    to_ok = isinstance(expected_to_kind, tuple) and target_kind in expected_to_kind or target_kind == expected_to_kind
                    if source_kind and not from_ok:
                        type_mismatches.append((filename, line_number, edge_type, "source", source_id, source_kind, expected_from_kind))
                    if target_kind and not to_ok:
                        type_mismatches.append((filename, line_number, edge_type, "target", target_id, target_kind, expected_to_kind))

    return orphans, type_mismatches


def main():
    if len(sys.argv) < 2:
        print("Usage: python3 check_debug_data.py <project_path>")
        sys.exit(1)

    project_path = sys.argv[1]
    debug_dir = find_debug_dir(project_path)

    print(f"Checking: {debug_dir}\n")

    # Check duplicates
    id_to_kind, duplicates = collect_node_ids(debug_dir)

    print("=== Duplicate ID Check ===")
    if duplicates:
        print(f"❌ {len(duplicates)} duplicates found:")
        for node_id, kind, sources in duplicates[:20]:
            locations = ", ".join(f"{f}:{line}" for f, line in sources)
            print(f"  [{kind}] {node_id} ({len(sources)} times, in {locations})")
        if len(duplicates) > 20:
            print(f"  ... and {len(duplicates) - 20} more")
    else:
        print("✅ No duplicates")

    print()

    # Check edges
    orphans, type_mismatches = check_edges(debug_dir, id_to_kind)

    print("=== Orphan Edge Check ===")
    if orphans:
        print(f"❌ {len(orphans)} orphan edges found:")
        for filename, line_number, side, node_id in orphans[:20]:
            print(f"  {filename}:{line_number} {side}={node_id}")
        if len(orphans) > 20:
            print(f"  ... and {len(orphans) - 20} more")
    else:
        print("✅ No orphan edges")

    print()

    print("=== Type Mismatch Check ===")
    if type_mismatches:
        print(f"❌ {len(type_mismatches)} type mismatches found:")
        for filename, line_number, edge_type, side, node_id, actual_kind, expected_kind in type_mismatches[:20]:
            print(f"  {filename}:{line_number} {edge_type} {side}={node_id} is {actual_kind}, expected {expected_kind}")
        if len(type_mismatches) > 20:
            print(f"  ... and {len(type_mismatches) - 20} more")
    else:
        print("✅ No type mismatches")

    print()
    print("=== Summary ===")
    print(f"Total node IDs: {len(id_to_kind)}")
    print(f"Duplicates: {len(duplicates)}")
    print(f"Orphan edges: {len(orphans)}")
    print(f"Type mismatches: {len(type_mismatches)}")

    if duplicates or orphans or type_mismatches:
        sys.exit(1)


if __name__ == "__main__":
    main()
