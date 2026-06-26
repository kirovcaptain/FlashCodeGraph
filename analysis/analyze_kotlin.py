#!/usr/bin/env python3
"""
FCG Kotlin Quality Analyzer — call resolution quality analysis for Kotlin (Android) projects.

Kotlin-specific: drop reason classification based on Kotlin/Android ecosystem patterns
(coroutines, scope functions, Kotlin stdlib, Android framework, Compose, etc.).

Commands:
  report        Full analysis: summary + all dimensions + comparison with last report (default)
  low           Low confidence deep analysis
  completeness  Dropped calls analysis
  unresolved    Unresolved hints analysis

Usage:
    python3 analyze_kotlin.py /path/to/project [command] [--compare N]
"""
import csv
import sys
import os
import re
import glob
import subprocess
import shutil
from collections import defaultdict
from datetime import datetime

csv.field_size_limit(10 * 1024 * 1024)

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REPORT_DIR = os.path.join(SCRIPT_DIR, "analysisReport")

# ── Helpers ──

def load_csv(path):
    if not os.path.exists(path):
        return []
    with open(path, encoding="utf-8", errors="replace") as f:
        return list(csv.DictReader(f))

def load_debug_calls(project):
    debug_dir = os.path.join(project, ".fcg", "debug")
    if not os.path.isdir(debug_dir):
        return []
    rows = []
    for f in sorted(os.listdir(debug_dir)):
        if f.startswith("calls_") and f.endswith(".csv"):
            rows.extend(load_csv(os.path.join(debug_dir, f)))
    return rows

def load_debug_rawcalls(project):
    debug_dir = os.path.join(project, ".fcg", "debug")
    if not os.path.isdir(debug_dir):
        return []
    rows = []
    for f in sorted(os.listdir(debug_dir)):
        if f.startswith("rawcalls") and f.endswith(".csv"):
            rows.extend(load_csv(os.path.join(debug_dir, f)))
    return rows

def load_debug_hints(project):
    path = os.path.join(project, ".fcg", "debug", "hints.csv")
    return load_csv(path)

def read_source_line(project, fp, line):
    try:
        with open(os.path.join(project, fp), encoding="utf-8", errors="replace") as f:
            lines = f.readlines()
            if 1 <= line <= len(lines):
                return lines[line - 1].strip()
    except FileNotFoundError:
        pass
    return ""

def save_report(content, prefix, project):
    os.makedirs(REPORT_DIR, exist_ok=True)
    project_name = os.path.basename(os.path.normpath(project))
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    path = os.path.join(REPORT_DIR, f"{prefix}_{project_name}_{timestamp}.txt")
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)
    return path

def find_recent_reports(prefix, project, count=1):
    project_name = os.path.basename(os.path.normpath(project))
    pattern = os.path.join(REPORT_DIR, f"{prefix}_{project_name}_*.txt")
    files = sorted(glob.glob(pattern))
    return files[-count:] if len(files) >= count else files

def parse_report_metrics(path):
    metrics = {}
    if not path or not os.path.exists(path):
        return metrics
    with open(path) as f:
        for line in f:
            line = line.strip()
            if "Low confidence" in line and "0.5" in line:
                m = re.search(r':\s*(\d+)', line)
                if m:
                    metrics["low_confidence"] = int(m.group(1))
            elif "Total CALLS edges:" in line:
                m = re.search(r'(\d+)', line)
                if m:
                    metrics["total_calls"] = int(m.group(1))
            elif "Completeness:" in line:
                m = re.search(r'([\d.]+)%', line)
                if m:
                    metrics["completeness"] = float(m.group(1))
            elif "Dropped (lost):" in line:
                m = re.search(r'(\d+)\s*calls', line)
                if m:
                    metrics["dropped"] = int(m.group(1))
            elif "Unresolved hints:" in line:
                m = re.search(r'(\d+)', line)
                if m:
                    metrics["unresolved"] = int(m.group(1))
    return metrics

# ── Kotlin Drop Reason Classification ──

KOTLIN_STDLIB_METHODS = {
    "let", "apply", "also", "run", "with", "takeIf", "takeUnless",
    "toString", "hashCode", "equals", "copy",
}
COLLECTION_METHODS = {
    "map", "filter", "forEach", "flatMap", "first", "firstOrNull", "last", "lastOrNull",
    "find", "any", "all", "none", "count", "sortedBy", "sortedByDescending",
    "groupBy", "associate", "associateBy", "zip", "unzip", "partition",
    "reduce", "fold", "sumOf", "maxOf", "minOf", "toList", "toSet", "toMap",
    "distinct", "distinctBy", "take", "drop", "chunked", "windowed",
}
COROUTINE_METHODS = {
    "launch", "async", "withContext", "coroutineScope", "supervisorScope",
    "emit", "collect", "collectLatest", "combine", "stateIn", "shareIn",
    "flowOn", "map", "filter", "onEach", "catch", "launchIn",
    "awaitClose", "trySend", "send", "receive",
}
ANDROID_FRAMEWORK_METHODS = {
    "findViewById", "setContentView", "inflate", "getSystemService",
    "startActivity", "finish", "runOnUiThread", "postDelayed",
    "observe", "observeForever", "removeObservers",
    "navigate", "popBackStack", "navigateUp",
    "setOnClickListener", "setAdapter", "notifyDataSetChanged",
}
COMPOSE_METHODS = {
    "remember", "mutableStateOf", "derivedStateOf", "collectAsState",
    "collectAsStateWithLifecycle", "LaunchedEffect", "DisposableEffect",
    "SideEffect", "rememberCoroutineScope", "hiltViewModel",
}
LOG_METHODS = {"d", "e", "i", "v", "w", "wtf", "println", "print"}

def classify_drop_reason(called_name, receiver_expr):
    if not called_name:
        return "unknown"
    if called_name in LOG_METHODS and receiver_expr in ("Log", "Timber", "logger", ""):
        return "logging"
    if called_name in KOTLIN_STDLIB_METHODS:
        return "kotlin_stdlib"
    if called_name in COLLECTION_METHODS:
        return "collection_method"
    if called_name in COROUTINE_METHODS:
        return "coroutine"
    if called_name in ANDROID_FRAMEWORK_METHODS:
        return "android_framework"
    if called_name in COMPOSE_METHODS:
        return "compose"
    if called_name[0:1].isupper():
        return "constructor"
    if receiver_expr == "":
        return "no_receiver"
    if "." in receiver_expr or "(" in receiver_expr:
        return "chained_call"
    if receiver_expr == "super":
        return "super_call"
    if receiver_expr in ("this", "it"):
        return "this_or_it"
    return "receiver_type_unknown"

# ── Unresolved Hint Classification ──

def classify_hint(called_name, receiver_expr, hint_type):
    if hint_type == "ambiguous_project_call":
        return "ambiguous_project"
    if hint_type == "lambda_call":
        if called_name in COROUTINE_METHODS:
            return "lambda_coroutine"
        if called_name in COLLECTION_METHODS:
            return "lambda_collection"
        return "lambda_other"
    if hint_type == "chained_call":
        return "chained"
    if hint_type == "untyped_receiver":
        return "untyped_receiver"
    return hint_type or "unknown"

# ── Low Confidence Analysis ──

def analyze_low(project, rows):
    if not rows:
        print("  No debug data found. Run 'fcg index --debug' first.")
        return
    low = [r for r in rows if 0 < float(r.get("confidence", 0)) < 0.5]
    sites = defaultdict(list)
    for r in low:
        sites[(r.get("source_id", ""), r["file_path"], r["line"])].append(r)

    print(f"  Total calls: {len(rows)}")
    print(f"  Low confidence (< 0.5): {len(low)} ({len(sites)} unique sites)")
    print()

    # By resolved_by
    by_type = defaultdict(int)
    for r in low:
        by_type[r["resolved_by"]] += 1
    print("  By resolved_by:")
    for rtype, cnt in sorted(by_type.items(), key=lambda x: -x[1]):
        print(f"    {rtype}: {cnt}")
    print()

    # By called method
    by_method = defaultdict(int)
    for r in low:
        by_method[r.get("called_name", "") or "<unknown>"] += 1
    print("  Top low-confidence methods:")
    for m, cnt in sorted(by_method.items(), key=lambda x: -x[1])[:15]:
        print(f"    {m}: {cnt}")
    print()

    # By receiver
    by_receiver = defaultdict(int)
    for r in low:
        receiver = r.get("receiver_expr", "") or "<none>"
        by_receiver[receiver] += 1
    print("  Top low-confidence receivers:")
    for receiver, cnt in sorted(by_receiver.items(), key=lambda x: -x[1])[:15]:
        print(f"    {receiver}: {cnt}")
    print()

    # Candidates distribution
    candidates_distribution = defaultdict(int)
    for _, targets in sites.items():
        candidates_distribution[int(targets[0].get("candidates", 0))] += 1
    print("  Candidates distribution:")
    for candidate_count, site_count in sorted(candidates_distribution.items()):
        print(f"    candidates={candidate_count}: {site_count} sites")
    print()

    # Samples
    print("  Samples (first 15 sites):")
    for (_, filepath, line_number), targets in list(sites.items())[:15]:
        code = read_source_line(project, filepath, int(line_number))
        print(f"    {filepath}:{line_number} cands={targets[0].get('candidates','')} {targets[0]['resolved_by']}")
        print(f"      {code}")
    if len(sites) > 15:
        print(f"    ... +{len(sites) - 15} more sites")
    print()

# ── Completeness Analysis ──

def analyze_completeness(project):
    rawcalls = load_debug_rawcalls(project)
    calls = load_debug_calls(project)
    hints = load_debug_hints(project)
    if not rawcalls:
        print("  No rawcalls data found. Run 'fcg index --debug' first.")
        return

    resolved_keys = set()
    for call in calls:
        resolved_keys.add((call["file_path"], call["line"]))
    for hint in hints:
        resolved_keys.add((hint["file_path"], hint["line"]))

    dropped = [r for r in rawcalls if (r["file_path"], r["line"]) not in resolved_keys]
    dropped_sites = len(set((d["file_path"], d["line"]) for d in dropped))
    resolved_sites = len(set((c["file_path"], c["line"]) for c in calls))
    hinted_sites = len(set((h["file_path"], h["line"]) for h in hints))
    completeness = len(resolved_keys) / max(len(rawcalls), 1) * 100

    print(f"  Raw calls (parser):    {len(rawcalls)}")
    print(f"  Resolved (CALLS):      {len(calls)} edges, {resolved_sites} sites")
    print(f"  Hinted (UNRESOLVED):   {len(hints)} edges, {hinted_sites} sites")
    print(f"  Dropped (lost):        {len(dropped)} calls, {dropped_sites} sites")
    print(f"  Completeness:          {completeness:.1f}%")
    print()

    if not dropped:
        print("  ✅ No dropped calls")
        return

    # Drop reasons (Kotlin-specific)
    by_reason = defaultdict(int)
    for d in dropped:
        receiver = d.get("receiver_expr", "") or ""
        called = d.get("called_name", "") or ""
        reason = classify_drop_reason(called, receiver)
        by_reason[reason] += 1

    print("  Drop reasons:")
    for reason, count in sorted(by_reason.items(), key=lambda x: -x[1]):
        print(f"    {reason}: {count}")
    print()

    # Top dropped methods
    by_method = defaultdict(int)
    for d in dropped:
        by_method[d.get("called_name", "") or "<unknown>"] += 1
    print("  Top dropped methods:")
    for method, count in sorted(by_method.items(), key=lambda x: -x[1])[:20]:
        print(f"    {method}: {count}")
    print()

    # Top dropped receivers
    by_receiver = defaultdict(int)
    for d in dropped:
        receiver = d.get("receiver_expr", "") or "<none>"
        by_receiver[receiver] += 1
    print("  Top dropped receivers:")
    for receiver, count in sorted(by_receiver.items(), key=lambda x: -x[1])[:20]:
        print(f"    {receiver}: {count}")
    print()

    # Samples
    print("  Dropped call samples:")
    seen = set()
    sample_count = 0
    for d in dropped:
        key = (d["file_path"], d["line"])
        if key in seen:
            continue
        seen.add(key)
        code = read_source_line(project, d["file_path"], int(d["line"]))
        receiver = d.get("receiver_expr", "")
        called = d.get("called_name", "")
        prefix = f"{receiver}." if receiver else ""
        print(f"    {d['file_path']}:{d['line']} {prefix}{called}() args={d.get('arg_count','')}")
        print(f"      {code}")
        sample_count += 1
        if sample_count >= 20:
            remaining = dropped_sites - sample_count
            if remaining > 0:
                print(f"    ... +{remaining} more")
            break
    print()

# ── Unresolved Hints Analysis ──

def analyze_unresolved(project):
    hints = load_debug_hints(project)
    if not hints:
        print("  No unresolved hints found.")
        return

    print(f"  Unresolved hints: {len(hints)}")
    print()

    # By hint_type
    by_type = defaultdict(int)
    for h in hints:
        by_type[h.get("hint_type", "unknown")] += 1
    print("  By hint type:")
    for hint_type, count in sorted(by_type.items(), key=lambda x: -x[1]):
        print(f"    {hint_type}: {count}")
    print()

    # By classification
    by_class = defaultdict(int)
    for h in hints:
        classification = classify_hint(
            h.get("called_name", ""),
            h.get("receiver_expr", ""),
            h.get("hint_type", "")
        )
        by_class[classification] += 1
    print("  By classification:")
    for classification, count in sorted(by_class.items(), key=lambda x: -x[1]):
        print(f"    {classification}: {count}")
    print()

    # By candidate count
    by_candidates = defaultdict(int)
    for h in hints:
        by_candidates[h.get("candidate_count", "?")] += 1
    print("  By candidate count:")
    for candidate_count, count in sorted(by_candidates.items(), key=lambda x: -x[1]):
        print(f"    {candidate_count} candidates: {count}")
    print()

    # Samples
    print("  Samples:")
    for h in hints[:15]:
        code = read_source_line(project, h["file_path"], int(h["line"]))
        receiver = h.get("receiver_expr", "")
        called = h.get("called_name", "")
        prefix = f"{receiver}." if receiver else ""
        print(f"    {h['file_path']}:{h['line']} {prefix}{called}() type={h.get('hint_type','')}")
        print(f"      {code}")
    if len(hints) > 15:
        print(f"    ... +{len(hints) - 15} more")
    print()

# ── Summary Report ──

def print_summary(project, rows):
    total = len(rows)
    low = len([r for r in rows if 0 < float(r.get("confidence", 0)) < 0.5])
    high_confidence_pct = (1 - low / max(total, 1)) * 100

    confidence_buckets = defaultdict(int)
    for r in rows:
        c = float(r.get("confidence", 0))
        if c >= 0.95: confidence_buckets["0.95 (exact)"] += 1
        elif c >= 0.85: confidence_buckets["0.85 (arg)"] += 1
        elif c >= 0.70: confidence_buckets["0.70 (external)"] += 1
        elif c >= 0.50: confidence_buckets["0.50+ (other)"] += 1
        else: confidence_buckets["<0.50 (low)"] += 1

    strategy_counts = defaultdict(int)
    for r in rows:
        strategy_counts[r["resolved_by"]] += 1

    print(f"  Total CALLS edges:      {total}")
    print(f"  High confidence (≥0.5): {total - low} ({high_confidence_pct:.1f}%)")
    print(f"  Low confidence (<0.5):  {low}")
    print()
    print("  Confidence distribution:")
    for bucket in ["0.95 (exact)", "0.85 (arg)", "0.70 (external)", "0.50+ (other)", "<0.50 (low)"]:
        if bucket in confidence_buckets:
            print(f"    {bucket}: {confidence_buckets[bucket]}")
    print()
    print("  Resolution strategy:")
    for strategy, count in sorted(strategy_counts.items(), key=lambda x: -x[1]):
        print(f"    {strategy}: {count} ({count/max(total,1)*100:.1f}%)")
    print()

def print_comparison(project, compare_count=1):
    reports = find_recent_reports("report", project, compare_count)
    if not reports:
        print("  No previous report found for comparison.")
        return

    rows = load_debug_calls(project)
    low = len([r for r in rows if 0 < float(r.get("confidence", 0)) < 0.5])
    rawcalls = load_debug_rawcalls(project)
    hints = load_debug_hints(project)
    resolved_keys = set()
    for c in rows:
        resolved_keys.add((c["file_path"], c["line"]))
    for h in hints:
        resolved_keys.add((h["file_path"], h["line"]))
    completeness = len(resolved_keys) / max(len(rawcalls), 1) * 100 if rawcalls else 0
    dropped = [r for r in rawcalls if (r["file_path"], r["line"]) not in resolved_keys]
    current = {"total_calls": len(rows), "low_confidence": low, "completeness": completeness, "dropped": len(dropped)}

    metrics_keys = [("total_calls", "Total CALLS"), ("low_confidence", "Low confidence"), ("completeness", "Completeness %"), ("dropped", "Dropped calls")]

    columns = []
    for report_path in reports:
        metrics = parse_report_metrics(report_path)
        if metrics:
            columns.append((os.path.basename(report_path), metrics))
    columns.append(("Current", current))

    col_width = 12
    header = f"  {'Metric':<25}"
    for name, _ in columns:
        timestamp = name
        match = re.search(r'(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})', name)
        if match:
            timestamp = f"{match.group(2)}-{match.group(3)} {match.group(4)}:{match.group(5)}"
        if name == "Current":
            timestamp = "Current"
        header += f" {timestamp:>{col_width}}"
    if len(columns) >= 2:
        header += f" {'Delta':>{col_width}}"
    print(header)
    print(f"  {'-' * (25 + (col_width + 1) * len(columns) + (col_width + 1 if len(columns) >= 2 else 0))}")

    for key, label in metrics_keys:
        row = f"  {label:<25}"
        values = []
        for _, metrics in columns:
            value = metrics.get(key, "—")
            values.append(value)
            if key == "completeness" and isinstance(value, float):
                row += f" {value:>{col_width}.1f}"
            elif isinstance(value, (int, float)):
                row += f" {value:>{col_width}}"
            else:
                row += f" {str(value):>{col_width}}"
        if len(values) >= 2 and isinstance(values[-2], (int, float)) and isinstance(values[-1], (int, float)):
            delta = values[-1] - values[-2]
            sign = "+" if delta > 0 else ""
            if key == "completeness":
                row += f" {sign}{delta:>{col_width - 1}.1f}"
            else:
                row += f" {sign}{delta:>{col_width - 1}}"
        elif len(columns) >= 2:
            row += f" {'—':>{col_width}}"
        print(row)
    print()

# ── Commands ──

def cmd_report(project, compare_count=1):
    rows = load_debug_calls(project)
    if not rows:
        print("No debug data found. Run 'fcg index --debug' first.")
        return

    print("=" * 70)
    print("  FCG Kotlin Quality Report")
    print(f"  Project: {project}")
    print(f"  Time: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 70)

    print(f"\n## Summary\n")
    print_summary(project, rows)

    print(f"## 1. Low Confidence Detail\n")
    analyze_low(project, rows)

    print(f"## 2. Completeness Detail\n")
    analyze_completeness(project)

    print(f"## 3. Unresolved Hints\n")
    analyze_unresolved(project)

    print(f"## 4. Comparison\n")
    print_comparison(project, compare_count)

def cmd_low(project):
    rows = load_debug_calls(project)
    print("=" * 70)
    print("  Low Confidence Analysis")
    print(f"  Project: {project}")
    print("=" * 70)
    print()
    analyze_low(project, rows)

def cmd_completeness(project):
    print("=" * 70)
    print("  Completeness Analysis")
    print(f"  Project: {project}")
    print("=" * 70)
    print()
    analyze_completeness(project)

def cmd_unresolved(project):
    print("=" * 70)
    print("  Unresolved Hints Analysis")
    print(f"  Project: {project}")
    print("=" * 70)
    print()
    analyze_unresolved(project)

# ── Main ──

COMMANDS = {
    "report": cmd_report,
    "low": cmd_low,
    "completeness": cmd_completeness,
    "unresolved": cmd_unresolved,
}

def main():
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} /path/to/project [report|low|completeness|unresolved] [--compare N]")
        sys.exit(1)

    project = sys.argv[1]
    command = sys.argv[2] if len(sys.argv) > 2 and not sys.argv[2].startswith("-") else "report"

    compare_count = 1
    if "--compare" in sys.argv:
        idx = sys.argv.index("--compare")
        if idx + 1 < len(sys.argv):
            compare_count = min(int(sys.argv[idx + 1]), 3)

    if command not in COMMANDS:
        print(f"Unknown command: {command}")
        print(f"Available: {', '.join(COMMANDS.keys())}")
        sys.exit(1)

    import io
    buf = io.StringIO()
    old_stdout = sys.stdout
    sys.stdout = buf
    if command == "report":
        COMMANDS[command](project, compare_count)
    else:
        COMMANDS[command](project)
    sys.stdout = old_stdout
    content = buf.getvalue()
    print(content, end="")

    path = save_report(content, command, project)
    print(f"📄 Report saved to {path}")

if __name__ == "__main__":
    main()
