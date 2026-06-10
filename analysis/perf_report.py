#!/usr/bin/env python3
"""
FCG Index Performance Report — analyze profiling data from `fcg index --profile`.

Usage:
    python3 perf_report.py /path/to/project

Reads from {project}/.fcg/profile/:
    - cpu.prof            CPU profile (analyzed via `go tool pprof`)
    - mem_trace.csv       Per-second heap sampling
    - phase_timeline.csv  Phase/sub-step start/end timestamps
"""
import csv
import os
import subprocess
import sys


def check_dependencies(profile_directory):
    """Check that all required files and tools exist."""
    required_files = ["cpu.prof", "mem_trace.csv", "phase_timeline.csv"]
    missing_files = []
    for filename in required_files:
        filepath = os.path.join(profile_directory, filename)
        if not os.path.exists(filepath):
            missing_files.append(filepath)

    if missing_files:
        print("ERROR: Missing profile files:")
        for filepath in missing_files:
            print(f"  - {filepath}")
        print("\nRun: fcg index <project> --profile")
        sys.exit(1)

    # Check go tool pprof
    result = subprocess.run(["go", "tool", "pprof", "-h"], capture_output=True)
    if result.returncode != 0 and b"usage" not in result.stderr.lower():
        print("ERROR: 'go tool pprof' not available. Install Go first.")
        sys.exit(1)


def load_mem_trace(profile_directory):
    """Load mem_trace.csv and return list of dicts."""
    filepath = os.path.join(profile_directory, "mem_trace.csv")
    rows = []
    with open(filepath, "r") as file:
        reader = csv.DictReader(file)
        for row in reader:
            rows.append({
                "elapsed_sec": int(row["elapsed_sec"]),
                "heap_inuse_mb": int(row["heap_inuse_mb"]),
                "heap_sys_mb": int(row["heap_sys_mb"]),
                "num_gc": int(row["num_gc"]),
            })
    return rows


def load_phase_timeline(profile_directory):
    """Load phase_timeline.csv and return list of events."""
    filepath = os.path.join(profile_directory, "phase_timeline.csv")
    events = []
    with open(filepath, "r") as file:
        reader = csv.DictReader(file)
        for row in reader:
            events.append({
                "elapsed_sec": int(row["elapsed_sec"]),
                "event": row["event"].strip(),
                "name": row["name"].strip(),
                "detail": row["detail"].strip() if row["detail"] else "",
            })
    return events


def build_phases(timeline_events):
    """Build phase intervals from timeline events."""
    phases = []
    phase_stack = {}  # name -> start_sec
    last_event_time = 0

    for event in timeline_events:
        if event["event"] == "phase_start":
            phase_stack[event["name"]] = event["elapsed_sec"]
        elif event["event"] == "phase_end":
            name = event["name"]
            start = phase_stack.pop(name, None)
            if start is None:
                # No matching phase_start — infer from last event time
                start = last_event_time
            phases.append({
                "name": name,
                "start": start,
                "end": event["elapsed_sec"],
                "detail": event["detail"],
            })
        last_event_time = event["elapsed_sec"]

    return phases


def build_substeps(timeline_events):
    """Build sub-step intervals from timeline events."""
    substeps = []
    substep_stack = {}

    for event in timeline_events:
        if event["event"] == "substep_start":
            substep_stack[event["name"]] = event["elapsed_sec"]
        elif event["event"] == "substep_end":
            name = event["name"]
            start = substep_stack.pop(name, None)
            if start is not None:
                substeps.append({
                    "name": name,
                    "start": start,
                    "end": event["elapsed_sec"],
                    "detail": event["detail"],
                })

    return substeps


def analyze_phase_memory(phases, mem_trace):
    """For each phase, compute heap range and GC count."""
    results = []
    for phase in phases:
        samples_in_phase = [
            sample for sample in mem_trace
            if phase["start"] <= sample["elapsed_sec"] <= phase["end"]
        ]
        if not samples_in_phase:
            results.append({
                "name": phase["name"],
                "start": phase["start"],
                "end": phase["end"],
                "duration": phase["end"] - phase["start"],
                "heap_start_mb": 0,
                "heap_peak_mb": 0,
                "gc_count": 0,
            })
            continue

        heap_start = samples_in_phase[0]["heap_inuse_mb"]
        heap_peak = max(sample["heap_inuse_mb"] for sample in samples_in_phase)
        gc_start = samples_in_phase[0]["num_gc"]
        gc_end = samples_in_phase[-1]["num_gc"]

        results.append({
            "name": phase["name"],
            "start": phase["start"],
            "end": phase["end"],
            "duration": phase["end"] - phase["start"],
            "heap_start_mb": heap_start,
            "heap_peak_mb": heap_peak,
            "gc_count": gc_end - gc_start,
        })

    return results


def get_cpu_hotspots(profile_directory, top_count=10):
    """Extract call graph from pprof dot output and build a real call tree."""
    cpu_prof_path = os.path.join(profile_directory, "cpu.prof")
    try:
        result = subprocess.run(
            ["go", "tool", "pprof", "-dot", "-nodefraction=0",
             "-focus=kirovcaptain", cpu_prof_path],
            capture_output=True, text=True, timeout=30,
        )
        if not result.stdout:
            return None

        import re
        nodes = {}  # node_id -> {name, flat, cum}
        edges = []  # (from_id, to_id, weight)

        module_prefix = "github.com/kirovcaptain/FlashCodeGraph/internal/"

        for line in result.stdout.split("\n"):
            # Parse node: N1 [label="..." tooltip="full.name (cum)"]
            node_match = re.match(r'\s*(N\d+)\s+\[.*tooltip="([^"]+)"', line)
            if node_match:
                node_id = node_match.group(1)
                tooltip = node_match.group(2)
                # tooltip format: "full.package.Function (10.5s)"
                cum_match = re.search(r'\(([0-9.]+)s\)', tooltip)
                cum_sec = float(cum_match.group(1)) if cum_match else 0
                # Extract function name
                func_name = tooltip.split(" (")[0]
                nodes[node_id] = {"name": func_name, "cum": cum_sec}
                continue

            # Parse edge: N1 -> N2 [label=" 58.29s" ...]
            edge_match = re.match(r'\s*(N\d+)\s+->\s+(N\d+)\s+\[label="[^"]*?([0-9.]+)s"', line)
            if edge_match:
                from_id = edge_match.group(1)
                to_id = edge_match.group(2)
                weight = float(edge_match.group(3))
                edges.append((from_id, to_id, weight))

        if not nodes:
            return None

        # Filter to only FCG business nodes
        business_nodes = {
            node_id: info for node_id, info in nodes.items()
            if module_prefix in info["name"] or "tree-sitter" in info["name"]
        }

        # Build adjacency: parent -> [(child, weight)]
        children_map = {}
        for from_id, to_id, weight in edges:
            if from_id in business_nodes and to_id in business_nodes:
                children_map.setdefault(from_id, []).append((to_id, weight))

        # Find root (node with no incoming business edge)
        has_parent = set()
        for from_id, to_id, _ in edges:
            if from_id in business_nodes and to_id in business_nodes:
                has_parent.add(to_id)
        roots = [node_id for node_id in business_nodes if node_id not in has_parent]

        # Sort roots by cum descending, pick the top one (Indexer.Index or fullIndex)
        roots.sort(key=lambda node_id: business_nodes[node_id]["cum"], reverse=True)

        # DFS to build tree
        total_samples = 0
        # Get total from pprof output
        for line in result.stdout.split("\n"):
            total_match = re.search(r'Total samples = ([0-9.]+)s', line)
            if total_match:
                total_samples = float(total_match.group(1))
                break

        tree_lines = []
        visited = set()

        def short_name(full_name):
            """Shorten function name for display."""
            name = full_name.replace(module_prefix, "")
            name = name.replace("github.com/tree-sitter/go-tree-sitter.", "tree-sitter.")
            return name

        def walk_tree(node_id, depth):
            if node_id in visited:
                return
            visited.add(node_id)
            info = business_nodes[node_id]
            cum_pct = (info["cum"] / total_samples * 100) if total_samples > 0 else 0
            indent = "│   " * depth
            connector = "├─ " if depth > 0 else ""
            display_name = short_name(info["name"])
            tree_lines.append(
                f"{indent}{connector}{display_name:<45} {info['cum']:>6.1f}s ({cum_pct:>5.1f}%)"
            )
            # Sort children by weight descending
            children = children_map.get(node_id, [])
            children.sort(key=lambda pair: pair[1], reverse=True)
            for child_id, _ in children:
                if child_id in business_nodes and child_id not in visited:
                    walk_tree(child_id, depth + 1)

        for root_id in roots[:2]:  # Start from top 2 roots
            walk_tree(root_id, 0)

        return (tree_lines, business_nodes, total_samples) if tree_lines else None
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return None


def build_call_tree(cpu_hotspots_data):
    """Passthrough — tree is already built by get_cpu_hotspots."""
    return cpu_hotspots_data


def print_report(mem_trace, phases, substeps, phase_memory, cpu_hotspots):
    """Print the final performance report."""
    total_duration = mem_trace[-1]["elapsed_sec"] if mem_trace else 0
    peak_heap = max(sample["heap_inuse_mb"] for sample in mem_trace) if mem_trace else 0
    total_gc = mem_trace[-1]["num_gc"] if mem_trace else 0

    print("=" * 60)
    print("  FCG Index Performance Report")
    print("=" * 60)
    print()
    print(f"  Duration:   {total_duration}s")
    print(f"  Peak Heap:  {peak_heap}MB")
    print(f"  Total GC:   {total_gc}")
    print()

    # Phase breakdown
    print("-" * 60)
    print("  Phase Breakdown")
    print("-" * 60)
    print(f"  {'Phase':<25} {'Duration':>8} {'Heap Peak':>10} {'GC':>5}")
    print(f"  {'-'*25} {'-'*8} {'-'*10} {'-'*5}")
    for phase_info in phase_memory:
        duration_str = f"{phase_info['duration']}s"
        heap_str = f"{phase_info['heap_peak_mb']}MB"
        gc_str = str(phase_info["gc_count"])
        print(f"  {phase_info['name']:<25} {duration_str:>8} {heap_str:>10} {gc_str:>5}")
    print()

    # Sub-step details
    if substeps:
        print("-" * 60)
        print("  Sub-step Details")
        print("-" * 60)
        print(f"  {'sub-step':<25} {'time':>5} {'heap peak':>10} {'GC':>4}  {'detail'}")
        print(f"  {'-'*25} {'-'*5} {'-'*10} {'-'*4}  {'-'*8}")
        for substep in substeps:
            duration = substep["end"] - substep["start"]
            detail = substep["detail"] if substep["detail"] else ""
            # Find memory stats for this sub-step's time range
            samples_in_range = [
                sample for sample in mem_trace
                if substep["start"] <= sample["elapsed_sec"] <= substep["end"]
            ]
            if samples_in_range:
                heap_peak = max(s["heap_inuse_mb"] for s in samples_in_range)
                gc_count = samples_in_range[-1]["num_gc"] - samples_in_range[0]["num_gc"]
                print(f"  {substep['name']:<25} {duration:>4}s {heap_peak:>7}MB {gc_count:>4}  {detail}")
            else:
                print(f"  {substep['name']:<25} {duration:>4}s {'-':>10} {'-':>4}  {detail}")
        print()

    # CPU hotspots
    if cpu_hotspots:
        tree_lines, business_nodes, total_samples = cpu_hotspots
        print("-" * 60)
        print("  CPU Call Tree (business code)")
        print("-" * 60)
        for line in tree_lines:
            print(f"  {line}")
        print()

        # Top 10 by cum time
        sorted_entries = sorted(business_nodes.values(), key=lambda entry: entry["cum"], reverse=True)
        print(f"  Top 10 by cumulative time")
        print(f"  {'function':<55} {'cum':>7} {'cum%':>6}")
        print(f"  {'-'*55} {'-'*7} {'-'*6}")
        module_prefix = "github.com/kirovcaptain/FlashCodeGraph/internal/"
        for entry in sorted_entries[:10]:
            short_name = entry["name"].replace(module_prefix, "")
            short_name = short_name.replace("github.com/tree-sitter/go-tree-sitter.", "tree-sitter.")
            cum_pct = (entry["cum"] / total_samples * 100) if total_samples > 0 else 0
            print(f"  {short_name:<55} {entry['cum']:>6.1f}s {cum_pct:>5.1f}%")
        print()
    else:
        print("-" * 60)
        print("  CPU Call Tree: no data (go tool pprof failed)")
        print("-" * 60)
        print()

    # Diagnosis
    print("-" * 60)
    print("  Diagnosis")
    print("-" * 60)

    # GC assessment
    gc_per_sec = total_gc / total_duration if total_duration > 0 else 0
    if gc_per_sec > 2:
        print(f"  ⚠️  GC pressure HIGH: {total_gc} GCs ({gc_per_sec:.1f}/sec)")
    elif gc_per_sec > 0.5:
        print(f"  ⚠️  GC pressure moderate: {total_gc} GCs ({gc_per_sec:.1f}/sec)")
    else:
        print(f"  ✅ GC pressure low: {total_gc} GCs ({gc_per_sec:.2f}/sec)")

    # Memory assessment
    if peak_heap > 4000:
        print(f"  ⚠️  Memory: peak {peak_heap}MB — consider increasing memory_limit")
    else:
        print(f"  ✅ Memory: peak {peak_heap}MB")

    # Find slowest phase
    if phase_memory:
        slowest = max(phase_memory, key=lambda phase_info: phase_info["duration"])
        print(f"  📍 Slowest phase: {slowest['name']} ({slowest['duration']}s)")

    # Find most GC-heavy phase
    if phase_memory:
        gc_heavy = max(phase_memory, key=lambda phase_info: phase_info["gc_count"])
        if gc_heavy["gc_count"] > 0:
            gc_rate = gc_heavy["gc_count"] / gc_heavy["duration"] if gc_heavy["duration"] > 0 else 0
            print(f"  📍 Most GC-heavy phase: {gc_heavy['name']} ({gc_heavy['gc_count']} GCs, {gc_rate:.1f}/sec)")

    print()
    print("=" * 60)


def main():
    if len(sys.argv) < 2:
        print("Usage: python3 perf_report.py /path/to/project")
        sys.exit(1)

    project_path = os.path.abspath(sys.argv[1])
    profile_directory = os.path.join(project_path, ".fcg", "profile")

    if not os.path.isdir(profile_directory):
        print(f"ERROR: Profile directory not found: {profile_directory}")
        print("Run: fcg index <project> --profile")
        sys.exit(1)

    check_dependencies(profile_directory)

    mem_trace = load_mem_trace(profile_directory)
    timeline_events = load_phase_timeline(profile_directory)
    phases = build_phases(timeline_events)
    substeps = build_substeps(timeline_events)
    phase_memory = analyze_phase_memory(phases, mem_trace)
    cpu_hotspots = get_cpu_hotspots(profile_directory)

    print_report(mem_trace, phases, substeps, phase_memory, cpu_hotspots)


if __name__ == "__main__":
    main()
