#!/usr/bin/env python3
"""
FCG TypeScript/JavaScript Quality Analyzer — call resolution quality analysis for TS/JS projects.

TS-specific: drop reason classification based on JS/TS ecosystem patterns
(Array methods, Map/Set, Promise chains, Node.js builtins, etc.).

Commands:
  report        Full analysis: summary + all dimensions + comparison with last report (default)
  low           Low confidence deep analysis
  completeness  Dropped calls analysis
  unresolved    Unresolved hints analysis (lambda_call, ambiguous, chained, etc.)

Usage:
    python3 analyze_ts.py /path/to/project [command] [--compare N]
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

def read_context(project, fp, line, before=5):
    try:
        with open(os.path.join(project, fp), encoding="utf-8", errors="replace") as f:
            lines = f.readlines()
            return [l.strip() for l in lines[max(0, line - before - 1):line]]
    except FileNotFoundError:
        return []

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

# ── TS/JS Drop Reason Classification ──

ARRAY_METHODS = {"push", "pop", "shift", "unshift", "splice", "slice", "map", "filter",
                 "reduce", "forEach", "find", "findIndex", "some", "every", "includes",
                 "indexOf", "join", "flat", "flatMap", "sort", "reverse", "fill", "concat"}
MAP_SET_METHODS = {"get", "set", "has", "delete", "clear", "add", "keys", "values", "entries", "forEach", "size"}
STRING_METHODS = {"split", "trim", "replace", "replaceAll", "match", "startsWith", "endsWith",
                  "includes", "indexOf", "slice", "substring", "toLowerCase", "toUpperCase", "padStart", "padEnd"}
PROMISE_METHODS = {"then", "catch", "finally", "resolve", "reject", "all", "allSettled", "race", "any"}
NODE_BUILTINS = {"readFileSync", "writeFileSync", "existsSync", "mkdirSync", "readdirSync",
                 "statSync", "unlinkSync", "resolve", "join", "dirname", "basename", "extname",
                 "createReadStream", "createWriteStream", "readFile", "writeFile"}
CONSOLE_METHODS = {"log", "warn", "error", "info", "debug", "trace", "dir", "table"}
EXPRESS_METHODS = {"json", "status", "send", "redirect", "render", "set", "get", "use"}

def classify_drop_reason(called_name, receiver_expr):
    if not called_name:
        return "unknown"
    if called_name in CONSOLE_METHODS and receiver_expr in ("console", ""):
        return "console"
    if called_name in ("warn", "info", "error", "debug") and "log" in receiver_expr.lower():
        return "logger"
    if called_name in ARRAY_METHODS:
        return "array_method"
    if called_name in MAP_SET_METHODS:
        return "map_set_method"
    if called_name in STRING_METHODS:
        return "string_method"
    if called_name in PROMISE_METHODS:
        return "promise_method"
    if called_name in NODE_BUILTINS:
        return "node_builtin"
    if called_name in EXPRESS_METHODS and receiver_expr in ("res", "req", "app", "router"):
        return "express_http"
    if called_name[0:1].isupper():
        return "constructor"
    if receiver_expr == "" and called_name not in ("require", "import"):
        return "no_receiver"
    if "." in receiver_expr or "(" in receiver_expr:
        return "chained_call"
    if called_name in ("require", "import"):
        return "module_import"
    return "receiver_type_unknown"

# ── Unresolved Hint Classification ──

def classify_hint(called_name, receiver_expr, hint_type):
    """Classify an unresolved hint into a semantic category."""
    if hint_type == "lambda_call":
        if called_name in ARRAY_METHODS:
            return "lambda_array"
        if called_name in MAP_SET_METHODS:
            return "lambda_map_set"
        if called_name in ("warn", "info", "error", "debug", "log"):
            return "lambda_logger"
        if called_name in ("query", "run", "exec", "execute"):
            return "lambda_db"
        if called_name in ("push", "pop", "shift", "unshift"):
            return "lambda_array_mutate"
        if called_name in ("json", "status", "send", "text"):
            return "lambda_http_response"
        if "node" in receiver_expr.lower() or called_name in ("namedChild", "child", "walk", "text"):
            return "lambda_ast_node"
        return "lambda_other"
    if hint_type == "ambiguous_project_call":
        return "ambiguous_project"
    if hint_type == "chained_call":
        return "chained"
    if hint_type == "untyped_receiver":
        return "untyped_receiver"
    if hint_type == "enum_method":
        return "enum_method"
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
    by_recv = defaultdict(int)
    for r in low:
        recv = r.get("receiver_expr", "") or "<none>"
        by_recv[recv] += 1
    print("  Top low-confidence receivers:")
    for recv, cnt in sorted(by_recv.items(), key=lambda x: -x[1])[:15]:
        print(f"    {recv}: {cnt}")
    print()

    # Candidates distribution
    cand_dist = defaultdict(int)
    for _, tgts in sites.items():
        cand_dist[int(tgts[0].get("candidates", 0))] += 1
    print("  Candidates distribution:")
    for c, cnt in sorted(cand_dist.items()):
        print(f"    candidates={c}: {cnt} sites")
    print()

    # Samples
    print("  Samples (first 15 sites):")
    count = 0
    for (_, fp, ln), tgts in list(sites.items())[:15]:
        code = read_source_line(project, fp, int(ln))
        print(f"    {fp}:{ln} cands={tgts[0].get('candidates','')} {tgts[0]['resolved_by']}")
        print(f"      {code}")
        count += 1
    if len(sites) > 15:
        print(f"    ... +{len(sites) - 15} more sites")
    print()

# ── Completeness Analysis ──

def analyze_comp(project):
    rawcalls = load_debug_rawcalls(project)
    calls = load_debug_calls(project)
    hints = load_debug_hints(project)
    if not rawcalls:
        print("  No rawcalls data found. Run 'fcg index --debug' first.")
        return

    resolved_keys = set()
    for c in calls:
        resolved_keys.add((c["file_path"], c["line"]))
    for h in hints:
        resolved_keys.add((h["file_path"], h["line"]))

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

    # Drop reasons (TS-specific)
    by_reason = defaultdict(int)
    for d in dropped:
        recv = d.get("receiver_expr", "") or ""
        method = d.get("called_name", "") or ""
        by_reason[classify_drop_reason(method, recv)] += 1

    print("  Drop reasons:")
    for reason, cnt in sorted(by_reason.items(), key=lambda x: -x[1]):
        print(f"    {reason}: {cnt}")
    print()

    # Top dropped methods
    by_method = defaultdict(int)
    for d in dropped:
        by_method[d.get("called_name", "") or "<unknown>"] += 1
    print("  Top dropped methods:")
    for m, cnt in sorted(by_method.items(), key=lambda x: -x[1])[:15]:
        print(f"    {m}: {cnt}")
    print()

    # Samples
    print("  Dropped call samples:")
    seen = set()
    count = 0
    for d in dropped:
        key = (d["file_path"], d["line"])
        if key in seen:
            continue
        seen.add(key)
        code = read_source_line(project, d["file_path"], int(d["line"]))
        recv = d.get("receiver_expr", "")
        print(f"    {d['file_path']}:{d['line']} {recv}.{d['called_name']}() args={d.get('arg_count','')}")
        print(f"      {code}")
        count += 1
        if count >= 20:
            remaining = dropped_sites - count
            if remaining > 0:
                print(f"    ... +{remaining} more")
            break
    print()

# ── Unresolved Hints Analysis ──

def analyze_unresolved(project):
    hints = load_debug_hints(project)
    if not hints:
        print("  No hints data found. Run 'fcg index --debug' first.")
        return

    print(f"  Unresolved hints: {len(hints)}")
    print()

    # By hint_type
    by_type = defaultdict(int)
    for h in hints:
        by_type[h.get("hint_type", "unknown")] += 1
    print("  By hint_type:")
    for ht, cnt in sorted(by_type.items(), key=lambda x: -x[1]):
        print(f"    {ht}: {cnt}")
    print()

    # By semantic classification
    by_class = defaultdict(list)
    for h in hints:
        cls = classify_hint(h.get("called_name", ""), h.get("receiver_expr", ""), h.get("hint_type", ""))
        by_class[cls].append(h)
    print("  By semantic classification:")
    for cls, items in sorted(by_class.items(), key=lambda x: -len(x[1])):
        print(f"    {cls}: {len(items)}")
    print()

    # Top unresolved methods
    by_method = defaultdict(int)
    for h in hints:
        by_method[h.get("called_name", "") or "<unknown>"] += 1
    print("  Top unresolved methods:")
    for m, cnt in sorted(by_method.items(), key=lambda x: -x[1])[:20]:
        print(f"    {m}: {cnt}")
    print()

    # Top receivers
    by_recv = defaultdict(int)
    for h in hints:
        recv = h.get("receiver_expr", "") or "<none>"
        by_recv[recv] += 1
    print("  Top unresolved receivers:")
    for recv, cnt in sorted(by_recv.items(), key=lambda x: -x[1])[:20]:
        print(f"    {recv}: {cnt}")
    print()

    # By file (top files with most unresolved)
    by_file = defaultdict(int)
    for h in hints:
        by_file[h["file_path"]] += 1
    print("  Top files with unresolved calls:")
    for fp, cnt in sorted(by_file.items(), key=lambda x: -x[1])[:15]:
        print(f"    {fp}: {cnt}")
    print()

    # Samples per classification
    print("  Samples by classification:")
    for cls, items in sorted(by_class.items(), key=lambda x: -len(x[1])):
        print(f"    [{cls}] ({len(items)} hints)")
        for h in items[:5]:
            code = read_source_line(project, h["file_path"], int(h["line"]))
            recv = h.get("receiver_expr", "")
            print(f"      {h['file_path']}:{h['line']} {recv}.{h['called_name']}() cands={h.get('candidate_count','')}")
            print(f"        {code}")
        if len(items) > 5:
            print(f"      ... +{len(items) - 5} more")
    print()

# ── Summary Report ──

def print_summary(project, rows):
    total = len(rows)
    low = len([r for r in rows if 0 < float(r.get("confidence", 0)) < 0.5])
    pct = (1 - low / max(total, 1)) * 100

    conf_buckets = defaultdict(int)
    for r in rows:
        c = float(r.get("confidence", 0))
        if c >= 0.95: conf_buckets["0.95 (exact)"] += 1
        elif c >= 0.85: conf_buckets["0.85 (arg)"] += 1
        elif c >= 0.70: conf_buckets["0.70 (external)"] += 1
        elif c >= 0.50: conf_buckets["0.50+ (other)"] += 1
        else: conf_buckets["<0.50 (low)"] += 1

    rb_counts = defaultdict(int)
    for r in rows:
        rb_counts[r["resolved_by"]] += 1

    print(f"  Total CALLS edges:      {total}")
    print(f"  High confidence (≥0.5): {total - low} ({pct:.1f}%)")
    print(f"  Low confidence (<0.5):  {low}")
    print()
    print("  Confidence distribution:")
    for bucket in ["0.95 (exact)", "0.85 (arg)", "0.70 (external)", "0.50+ (other)", "<0.50 (low)"]:
        if bucket in conf_buckets:
            print(f"    {bucket}: {conf_buckets[bucket]}")
    print()
    print("  Resolution strategy:")
    for rb, cnt in sorted(rb_counts.items(), key=lambda x: -x[1]):
        print(f"    {rb}: {cnt} ({cnt/max(total,1)*100:.1f}%)")
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
    current = {"total_calls": len(rows), "low_confidence": low, "completeness": completeness, "dropped": len(dropped), "unresolved": len(hints)}

    metrics_keys = [("total_calls", "Total CALLS"), ("low_confidence", "Low confidence"), ("completeness", "Completeness %"), ("dropped", "Dropped calls"), ("unresolved", "Unresolved hints")]

    columns = []
    for rpath in reports:
        m = parse_report_metrics(rpath)
        if m:
            columns.append((os.path.basename(rpath), m))
    columns.append(("Current", current))

    col_width = 12
    header = f"  {'Metric':<25}"
    for name, _ in columns:
        ts = name
        m = re.search(r'(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})', name)
        if m:
            ts = f"{m.group(2)}-{m.group(3)} {m.group(4)}:{m.group(5)}"
        if name == "Current":
            ts = "Current"
        header += f" {ts:>{col_width}}"
    if len(columns) >= 2:
        header += f" {'Delta':>{col_width}}"
    print(header)
    print(f"  {'-' * (25 + (col_width + 1) * len(columns) + (col_width + 1 if len(columns) >= 2 else 0))}")

    for key, label in metrics_keys:
        row = f"  {label:<25}"
        vals = []
        for _, m in columns:
            v = m.get(key, "—")
            vals.append(v)
            if key == "completeness" and isinstance(v, float):
                row += f" {v:>{col_width}.1f}"
            elif isinstance(v, (int, float)):
                row += f" {v:>{col_width}}"
            else:
                row += f" {str(v):>{col_width}}"
        if len(vals) >= 2 and isinstance(vals[-2], (int, float)) and isinstance(vals[-1], (int, float)):
            delta = vals[-1] - vals[-2]
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
    print("  FCG TypeScript/JavaScript Quality Report")
    print(f"  Project: {project}")
    print(f"  Time: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 70)

    print(f"\n## Summary\n")
    print_summary(project, rows)

    print(f"## 1. Low Confidence Detail\n")
    analyze_low(project, rows)

    print(f"## 2. Completeness Detail\n")
    analyze_comp(project)

    print(f"## 3. Unresolved Hints Detail\n")
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
    analyze_comp(project)

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
