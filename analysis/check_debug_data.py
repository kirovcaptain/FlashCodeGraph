#!/usr/bin/env python3
"""
check_debug_data.py — Detect duplicate IDs and orphan edges in FCG debug dump.

Usage:
    python3 analysis/check_debug_data.py /path/to/project
"""

import csv
import os
import sys
from collections import defaultdict


def find_debug_dir(project_path):
    debug_dir = os.path.join(project_path, ".fcg", "debug")
    if not os.path.isdir(debug_dir):
        print(f"❌ Debug directory not found: {debug_dir}")
        print("   Run: fcg index --debug --force")
        sys.exit(1)
    return debug_dir


def collect_node_ids(debug_dir):
    """Read all node CSVs and collect IDs. Returns (id_set, duplicates)."""
    node_files = [
        ("symbols.csv", 0),           # id in column 0
        ("routes.csv", 0),
        ("structural_nodes.csv", 0),
        ("external_nodes.csv", 0),
    ]

    all_ids = set()
    id_sources = defaultdict(list)  # id -> [(file, line)]
    duplicates = []

    for filename, id_column in node_files:
        filepath = os.path.join(debug_dir, filename)
        if not os.path.exists(filepath):
            continue
        with open(filepath, "r", encoding="utf-8") as f:
            reader = csv.reader(f)
            header = next(reader, None)
            if not header:
                continue
            for line_number, row in enumerate(reader, start=2):
                if len(row) <= id_column:
                    continue
                node_id = row[id_column]
                if node_id in all_ids:
                    id_sources[node_id].append((filename, line_number))
                else:
                    all_ids.add(node_id)
                    id_sources[node_id].append((filename, line_number))

    for node_id, sources in id_sources.items():
        if len(sources) > 1:
            duplicates.append((node_id, sources))

    return all_ids, duplicates


def check_orphan_edges(debug_dir, all_node_ids):
    """Read edge CSVs and check if source/target exist in node set."""
    edge_files = [
        ("structural_edges.csv", 0, 1),  # source_id col 0, target_id col 1
    ]

    # Also check calls_*.csv and hints.csv if they exist
    for filename in os.listdir(debug_dir):
        if filename.startswith("calls_") and filename.endswith(".csv"):
            edge_files.append((filename, 3, 4))  # source_id col 3, target_id col 4

    orphans = []
    for filename, source_column, target_column in edge_files:
        filepath = os.path.join(debug_dir, filename)
        if not os.path.exists(filepath):
            continue
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
                if source_id and source_id not in all_node_ids:
                    orphans.append((filename, line_number, "source", source_id))
                if target_id and target_id not in all_node_ids:
                    orphans.append((filename, line_number, "target", target_id))

    return orphans


def main():
    if len(sys.argv) < 2:
        print("Usage: python3 check_debug_data.py <project_path>")
        sys.exit(1)

    project_path = sys.argv[1]
    debug_dir = find_debug_dir(project_path)

    print(f"Checking: {debug_dir}\n")

    # Check duplicates
    all_node_ids, duplicates = collect_node_ids(debug_dir)

    print("=== Duplicate ID Check ===")
    if duplicates:
        print(f"❌ {len(duplicates)} duplicates found:")
        for node_id, sources in duplicates[:20]:
            locations = ", ".join(f"{f}:{line}" for f, line in sources)
            print(f"  {node_id} ({len(sources)} times, in {locations})")
        if len(duplicates) > 20:
            print(f"  ... and {len(duplicates) - 20} more")
    else:
        print("✅ No duplicates")

    print()

    # Check orphan edges
    orphans = check_orphan_edges(debug_dir, all_node_ids)

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
    print("=== Summary ===")
    print(f"Total node IDs: {len(all_node_ids)}")
    print(f"Duplicates: {len(duplicates)}")
    print(f"Orphan edges: {len(orphans)}")

    if duplicates or orphans:
        sys.exit(1)


if __name__ == "__main__":
    main()
