#!/usr/bin/env python3
"""memory_cli.py — 记忆系统命令行工具"""
import sys, argparse
sys.path.insert(0, str(__import__('pathlib').Path(__file__).parent))
from memory_api import ingest, recall, feedback, cleanup, VAULT, read_all_memories

def cmd_add(args):
    result = ingest(content=args.content, type=args.type, project=args.project,
                    tags=args.tags.split(",") if args.tags else None,
                    source=args.source)
    print(f"{'✅ accepted' if result['accepted'] else '📥 inbox'}: {result['reason']}")
    print(f"   id={result['id']} path={result['path']}")

def cmd_search(args):
    results = recall(query=args.query, project=args.project, type=args.type,
                     tags=args.tags.split(",") if args.tags else None,
                     min_score=args.min_score, limit=args.limit)
    if not results:
        print("No results."); return
    for r in results:
        print(f"[{r['type']}] {r['title']} (⭐{r['score']} {r['project']})")
        print(f"  {r['snippet'][:80]}...")
        print()

def cmd_list(args):
    results = recall(type=args.type, project=args.project,
                     status=args.status, limit=args.limit)
    for r in results:
        print(f"[{r['type']}] {r['title']} (⭐{r['score']} {r['project']})")

def cmd_feedback(args):
    result = feedback(args.id, used=True, helpful=args.helpful)
    print(result)

def cmd_cleanup(args):
    result = cleanup()
    print(f"Archived {result['archived']} memories")

def cmd_stats(args):
    all_mem = read_all_memories(status=None)
    active = [m for m in all_mem if m["fm"].get("status") == "active"]
    archived = [m for m in all_mem if m["fm"].get("status") == "archived"]
    by_type = {}
    for m in active:
        t = m["fm"].get("type", "unknown")
        by_type[t] = by_type.get(t, 0) + 1
    print(f"Total: {len(all_mem)} | Active: {len(active)} | Archived: {len(archived)}")
    print("By type:", by_type)

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers()
    p = sub.add_parser("add"); p.add_argument("content"); p.add_argument("--type"); p.add_argument("--project"); p.add_argument("--tags"); p.add_argument("--source", default="manual"); p.set_defaults(fn=cmd_add)
    p = sub.add_parser("search"); p.add_argument("--query"); p.add_argument("--project"); p.add_argument("--type"); p.add_argument("--tags"); p.add_argument("--min-score", type=float, default=0); p.add_argument("--limit", type=int, default=10); p.set_defaults(fn=cmd_search)
    p = sub.add_parser("list"); p.add_argument("--type"); p.add_argument("--project"); p.add_argument("--status", default="active"); p.add_argument("--limit", type=int, default=20); p.set_defaults(fn=cmd_list)
    p = sub.add_parser("feedback"); p.add_argument("id"); p.add_argument("--helpful", action="store_true"); p.set_defaults(fn=cmd_feedback)
    p = sub.add_parser("cleanup"); p.set_defaults(fn=cmd_cleanup)
    p = sub.add_parser("stats"); p.set_defaults(fn=cmd_stats)
    args = parser.parse_args()
    args.fn(args)
