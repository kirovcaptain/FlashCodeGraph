#!/usr/bin/env python3
"""
FCG Java Quality Analyzer — call resolution quality analysis for Java projects.

Java-specific: drop reason classification, overload analysis, and external call
origin detection are based on Java ecosystem patterns (JDK, Spring, Guava, etc.).

Commands:
  report        Full analysis: summary + all dimensions + comparison with last report (default)
  low           Low confidence deep analysis
  completeness  Dropped calls analysis
  external      External call origin analysis (requires FalkorDB)

Usage:
    python3 analyze_java.py /path/to/project [command] [--compare N]
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
REDIS_CLI = shutil.which("redis-cli") or "redis-cli"

# ── Helpers ──

def load_csv(path):
    if not os.path.exists(path):
        return []
    with open(path) as f:
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

def extract_method(code):
    m = re.search(r'\.(\w+)\s*\(', code)
    return m.group(1) if m else ""

LOG_RE = re.compile(r'\b(log|logger|loggerUtil|LOG)\s*\.\s*(error|warn|info|debug|trace)\s*\(', re.IGNORECASE)

def has_catch(ctx):
    return any(re.search(r'catch\s*\(', l) for l in ctx)

def save_report(content, prefix, project):
    os.makedirs(REPORT_DIR, exist_ok=True)
    project_name = os.path.basename(os.path.normpath(project))
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    path = os.path.join(REPORT_DIR, f"{prefix}_{project_name}_{timestamp}.txt")
    with open(path, "w") as f:
        f.write(content)
    return path

def find_recent_reports(prefix, project, count=1):
    """Find the most recent N reports (excluding the current one being saved)."""
    project_name = os.path.basename(os.path.normpath(project))
    pattern = os.path.join(REPORT_DIR, f"{prefix}_{project_name}_*.txt")
    files = sorted(glob.glob(pattern))
    # Return last N, most recent last
    return files[-count:] if len(files) >= count else files

def parse_report_metrics(path):
    """Extract key metrics from a saved report for comparison."""
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
    return metrics

def get_falkordb_uri(project):
    for config_path in [
        os.path.join(project, ".fcg", "config.toml"),
        os.path.expanduser("~/.fcg/config.toml"),
    ]:
        if os.path.exists(config_path):
            try:
                with open(config_path, encoding="utf-8") as f:
                    for line in f:
                        line = line.strip()
                        if line.startswith("falkordb_uri"):
                            return line.split("=", 1)[1].strip().strip('"').strip("'")
            except Exception:
                continue
    return "localhost:6379"

def redis_cmd(project):
    uri = get_falkordb_uri(project)
    if ":" in uri:
        host, port = uri.rsplit(":", 1)
        return [REDIS_CLI, "-h", host, "-p", port]
    return [REDIS_CLI, "-h", uri, "-p", "6379"]

# ── Low Confidence Analysis ──

def classify_overload(project, key, targets):
    sid, fp, ln = key
    code = read_source_line(project, fp, int(ln))
    ctx = read_context(project, fp, int(ln))
    rb = targets[0]["resolved_by"]
    if rb == "import_multi":
        m = extract_method(code)
        return f"import_{m}" if m else "import_unknown"
    if LOG_RE.search(code):
        return "logger_in_catch" if has_catch(ctx) else "logger_varargs"
    if has_catch(ctx):
        return "overload_in_catch"
    if re.search(r'\.get(Code|Value|Key)\(\)', code):
        return "enum_getter"
    if ".build()" in code or ".builder()" in code:
        return "builder"
    return "type_disambiguation"

def classify_site(targets):
    if len(targets) == 1 and targets[0]["resolved_by"] == "external":
        return "external"
    classes, pkgs = set(), set()
    for t in targets:
        parts = t["target_id"].rsplit(".", 1)
        if len(parts) == 2:
            cls = parts[0].rsplit(".", 1)[-1] if "." in parts[0] else parts[0]
            classes.add(cls)
            if "." in parts[0]:
                pkgs.add(parts[0][:parts[0].rfind(".")])
    if len(classes) == 1 and len(pkgs) > 1:
        return "cross_module_same_class"
    return "method_overload"

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

    # Classify
    classifications = defaultdict(list)
    sub_cls = defaultdict(list)
    for key, tgts in sites.items():
        c = classify_site(tgts)
        classifications[c].append((key, tgts))
        if c == "method_overload":
            sub_cls[classify_overload(project, key, tgts)].append((key, tgts))

    print("  By classification:")
    for c, s in sorted(classifications.items(), key=lambda x: -len(x[1])):
        edges = sum(len(t) for _, t in s)
        print(f"    {c}: {len(s)} sites, {edges} edges")
    print()

    if sub_cls:
        print("  Method overload detail:")
        for c, s in sorted(sub_cls.items(), key=lambda x: -len(x[1])):
            edges = sum(len(t) for _, t in s)
            print(f"    {c}: {len(s)} sites, {edges} edges")
        print()

    # Top methods
    methods = defaultdict(lambda: [0, 0])
    for (_, fp, ln), tgts in sites.items():
        m = extract_method(read_source_line(project, fp, int(ln)))
        if m:
            methods[m][0] += 1
            methods[m][1] += len(tgts)
    if methods:
        print("  Top unresolved methods:")
        for m, (s, e) in sorted(methods.items(), key=lambda x: -x[1][0])[:15]:
            print(f"    {m}: {s} sites, {e} edges")
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
    print("  Samples:")
    for sub, s in sorted(sub_cls.items(), key=lambda x: -len(x[1])):
        edges = sum(len(t) for _, t in s)
        print(f"    [{sub}] ({len(s)} sites, {edges} edges)")
        for (_, fp, ln), tgts in s[:5]:
            code = read_source_line(project, fp, int(ln))
            print(f"      {fp}:{ln} cands={tgts[0].get('candidates','')} {tgts[0]['resolved_by']}")
            print(f"        {code}")
        if len(s) > 5:
            print(f"      ... +{len(s)-5} more")
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

    # Drop reasons
    by_reason = defaultdict(int)
    for d in dropped:
        recv = d.get("receiver_expr", "") or ""
        method = d.get("called_name", "") or ""
        if not method: by_reason["unknown"] += 1
        elif recv in ("System.out", "System.err") or method in ("println", "printf", "print"): by_reason["jdk_io"] += 1
        elif recv == "System" or method in ("currentTimeMillis", "nanoTime", "exit", "getenv"): by_reason["jdk_system"] += 1
        elif method in ("IllegalArgumentException", "RuntimeException", "NullPointerException", "StringBuilder", "ArrayList", "HashMap", "HashSet"): by_reason["jdk_constructor"] += 1
        elif recv == "" and method and method[0].isupper(): by_reason["constructor"] += 1
        elif "." in recv or "(" in recv: by_reason["chained_call"] += 1
        elif recv == "": by_reason["no_receiver"] += 1
        elif any(recv.startswith(p) for p in ("Arrays", "Collections", "Objects", "Math", "Long", "Integer", "String", "Boolean", "Double")): by_reason["jdk_static"] += 1
        elif recv[0:1].isupper(): by_reason["class_static_call"] += 1
        elif recv in ("log", "logger", "LOG"): by_reason["slf4j_logger"] += 1
        elif method in ("append", "toString", "setLength", "replace", "insert", "delete", "substring", "charAt", "length", "indexOf"): by_reason["jdk_string_builder"] += 1
        elif method in ("getMessage", "printStackTrace", "getCause", "getStackTrace"): by_reason["exception_method"] += 1
        else: by_reason["receiver_type_unknown"] += 1

    print("  Drop reasons:")
    for reason, cnt in sorted(by_reason.items(), key=lambda x: -x[1]):
        print(f"    {reason}: {cnt}")
    print()

    # Top dropped
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

# ── External Analysis ──

JDK_PACKAGES = {"java.", "javax.", "sun.", "com.sun.", "jdk."}
COMMON_LIBS = {
    "org.apache.": "Apache Commons", "com.google.": "Guava",
    "com.alibaba.fastjson": "FastJSON", "com.fasterxml.jackson": "Jackson",
    "org.slf4j.": "SLF4J", "ch.qos.logback": "Logback",
    "org.springframework.": "Spring", "com.baomidou.mybatisplus": "MyBatis-Plus",
    "org.mybatis.": "MyBatis", "redis.clients.": "Jedis",
    "io.netty.": "Netty", "cn.hutool.": "Hutool", "org.redisson.": "Redisson",
}

def detect_graph_name(project):
    """Detect graph name matching FCG naming: fcg_{project}_{branch}."""
    project_name = os.path.basename(os.path.normpath(project))
    # Read .git/HEAD directly (same logic as FCG's DetectBranch)
    head_path = os.path.join(project, ".git", "HEAD")
    branch = "default"
    try:
        with open(head_path) as f:
            line = f.read().strip()
        if line.startswith("ref: refs/heads/"):
            branch = line[len("ref: refs/heads/"):]
        elif len(line) >= 8:
            branch = line[:8]
    except FileNotFoundError:
        pass
    return f"fcg_{project_name}_{branch}".replace("/", "_")
    cypher = f"MATCH (n) WHERE n.id = '{node_id}' RETURN n.name, n.qualified_name, n.file_path, n.kind LIMIT 1"
    r = subprocess.run(cmd_prefix + ["GRAPH.QUERY", graph, cypher], capture_output=True, text=True)
    vals = []
    for l in r.stdout.strip().split("\n"):
        l = l.strip()
        if not l or l.startswith("n.") or l.startswith("Cached") or l.startswith("Query"):
            continue
        vals.append(l)
    if len(vals) >= 4:
        return {"name": vals[0], "qn": vals[1], "file": vals[2], "kind": vals[3]}
    if len(vals) >= 2:
        return {"name": vals[0], "qn": vals[1], "file": "", "kind": ""}
    return None

def classify_origin(qn, file_path):
    if not qn:
        return "unknown"
    for prefix in JDK_PACKAGES:
        if qn.startswith(prefix):
            return "JDK"
    for prefix, lib in COMMON_LIBS.items():
        if qn.startswith(prefix):
            return lib
    if file_path and not file_path.startswith("/") and file_path.endswith(".java"):
        return "project"
    if file_path == "" or file_path == "<synthetic>":
        return "Lombok"
    return "third-party"

def analyze_ext(project, rows):
    if not rows:
        print("  No debug data found.")
        return

    # Auto-detect graph name
    graph = detect_graph_name(project)

    externals = [r for r in rows if r.get("resolved_by") == "external" and float(r.get("confidence", 1)) < 0.5]
    print(f"  External low-confidence calls: {len(externals)}")
    if not externals:
        print("  ✅ No external low-confidence calls")
        return
    print()

    cmd_prefix = redis_cmd(project)
    node_cache = {}
    def get_node(nid):
        if nid not in node_cache:
            node_cache[nid] = query_node(graph, nid, cmd_prefix)
        return node_cache[nid]

    by_origin = defaultdict(list)
    for r in externals:
        target = get_node(r["target_id"])
        if target:
            origin = classify_origin(target["qn"], target.get("file", ""))
        else:
            origin = "unknown"
        by_origin[origin].append(r)

    print("  By origin:")
    for origin, items in sorted(by_origin.items(), key=lambda x: -len(x[1])):
        print(f"    {origin}: {len(items)}")
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

    # Compute current metrics
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

    # Build column data: each previous report + current
    columns = []
    for rpath in reports:
        m = parse_report_metrics(rpath)
        if m:
            columns.append((os.path.basename(rpath), m))
    columns.append(("Current", current))

    # Header
    col_width = 12
    header = f"  {'Metric':<25}"
    for name, _ in columns:
        # Extract timestamp from filename: report_xxx_YYYYMMDD_HHMMSS.txt → MM-DD HH:MM
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

    # Rows
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
        # Delta: current vs previous (second to last column)
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
    print("  FCG Quality Report")
    print(f"  Project: {project}")
    print(f"  Time: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 70)

    print(f"\n## Summary\n")
    print_summary(project, rows)

    print(f"## 1. Low Confidence Detail\n")
    analyze_low(project, rows)

    print(f"## 2. Completeness Detail\n")
    analyze_comp(project)

    print(f"## 3. External Detail\n")
    analyze_ext(project, rows)

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

def cmd_external(project):
    rows = load_debug_calls(project)
    print("=" * 70)
    print("  External Call Analysis")
    print(f"  Project: {project}")
    print("=" * 70)
    print()
    analyze_ext(project, rows)

# ── Main ──

COMMANDS = {
    "report": cmd_report,
    "low": cmd_low,
    "completeness": cmd_completeness,
    "external": cmd_external,
}

def main():
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} /path/to/project [report|low|completeness|external] [--compare N]")
        sys.exit(1)

    project = sys.argv[1]
    command = sys.argv[2] if len(sys.argv) > 2 and not sys.argv[2].startswith("-") else "report"

    # Parse --compare N
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
