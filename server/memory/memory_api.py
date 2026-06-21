import os, re, yaml, hashlib, time
from pathlib import Path
from datetime import datetime, timedelta

VAULT = Path.home() / ".hermes" / "vault"
MEMORY_DIR = Path.home() / ".hermes" / "memory"

TYPE_TTL = {
    "decision": None,      # 永久
    "gotcha": None,
    "incident": 365,
    "snippet": 90,
    "requirement": 180,
    "meeting": 30,
    "agent-trace": 7,
}

def compute_id(content: str) -> str:
    """用内容 hash 生成短 id"""
    return "mem_" + hashlib.md5(content[:200].encode()).hexdigest()[:8]

def parse_frontmatter(text: str) -> tuple[dict, str]:
    """解析 yaml frontmatter 和正文"""
    match = re.match(r'^---\n(.*?)\n---\n(.*)', text, re.DOTALL)
    if match:
        return yaml.safe_load(match.group(1)), match.group(2)
    return {}, text

def write_memory(id: str, content: str, fm: dict, target_dir: Path):
    """写入记忆文件"""
    target_dir.mkdir(parents=True, exist_ok=True)
    path = target_dir / f"{id}.md"
    with open(path, "w") as f:
        f.write(f"---\n{yaml.dump(fm)}---\n{content}")
    return path

def read_all_memories(status="active"):
    """扫描所有记忆"""
    results = []
    for root in [VAULT/"notes", VAULT/"inbox", VAULT/"archive"]:
        if not root.exists(): continue
        for path in root.rglob("*.md"):
            fm, body = parse_frontmatter(path.read_text())
            if status and fm.get("status") != status: continue
            results.append({"path": str(path), "fm": fm, "body": body})
    return results

def jaccard_similarity(a: str, b: str) -> float:
    """简单词集合相似度"""
    set_a = set(re.findall(r'\w+', a.lower()))
    set_b = set(re.findall(r'\w+', b.lower()))
    if not set_a or not set_b: return 0
    return len(set_a & set_b) / len(set_a | set_b)

def auto_classify(content: str) -> dict:
    """分析内容，自动推断 type/project/tags"""
    text = content.lower()
    if any(w in text for w in ["bug", "error", "crash", "panic", "fail"]):
        mem_type = "incident"
    elif any(w in text for w in ["为什么选", "权衡", "决定", "采用", "strategy"]):
        mem_type = "decision"
    elif "```" in content or "`" in content:
        mem_type = "snippet"
    elif any(w in text for w in ["会议", "结论", "action", "对齐"]):
        mem_type = "meeting"
    elif any(w in text for w in ["需求", "prd", "feature", "requirement"]):
        mem_type = "requirement"
    elif any(w in text for w in ["踩坑", "坑", "约定", "注意", "gotcha"]):
        mem_type = "gotcha"
    else:
        mem_type = "agent-trace"

    # 简单 tags 提取（英文单词）
    tags = re.findall(r'\b[a-z]{3,}(?:\.[a-z]{2,})?\b', content.lower())
    tags = [t for t in tags if t not in ["the", "and", "for", "with", "from", "this", "that"]]

    # project 用当前目录名
    project = os.path.basename(os.getcwd()) or "general"

    return {"type": mem_type, "project": project, "tags": list(set(tags))[:5]}

def ingest(content: str, type: str = None, project: str = None,
           tags: list = None, source: str = "manual", title: str = None) -> dict:
    """打分入库"""
    auto = auto_classify(content)
    type = type or auto["type"]
    project = project or auto["project"]
    tags = tags or auto["tags"]

    title = title or content[:60].replace("\n", " ")
    id = compute_id(content)
    now = datetime.now().strftime("%Y-%m-%d")

    # 计算 novelty（与已有 active 记忆的相似度，取最高）
    existing = read_all_memories("active")
    novelty = 1.0
    if existing:
        scores = [jaccard_similarity(content, m["body"]) for m in existing]
        novelty = max(scores) if scores else 1.0
        novelty = 1.0 - novelty  # 越相似越低

    # 计算 specificity（是否含具体细节）
    specificity = 0.5
    if re.search(r'\d{4,}', content): specificity += 0.1  # 数字
    if re.search(r'```|`', content): specificity += 0.15  # 代码
    if any(w in content for w in ["因为", "所以", "决定", "原因", "why", "because"]): specificity += 0.15
    specificity = min(1.0, specificity)

    # 计算 reusability
    reusability = 0.5 + (len(tags) / 10)
    reusability = min(1.0, reusability)

    # 来源可信度
    source_score = {"manual": 1.0, "code-review": 0.9, "agent-run": 0.7, "debug-session": 0.8, "meeting": 0.6}.get(source, 0.5)

    # 综合分数
    score = 0.3*novelty + 0.3*specificity + 0.25*reusability + 0.15*source_score
    score = round(score * 5, 1)  # 换算到 0-5

    # 计算过期时间
    ttl = TYPE_TTL.get(type)
    if ttl:
        expires_at = (datetime.now() + timedelta(days=ttl)).strftime("%Y-%m-%d")
    else:
        expires_at = "9999-12-31"

    fm = {
        "id": id,
        "type": type,
        "project": project,
        "tags": tags,
        "title": title,
        "score": score,
        "novelty": round(novelty, 2),
        "specificity": round(specificity, 2),
        "reusability": round(reusability, 2),
        "hit_count": 0,
        "created_at": now,
        "expires_at": expires_at,
        "status": "active",
        "source": source,
        "links": [],
    }

    # 判断入库还是 inbox
    threshold = 3.0
    if score >= threshold:
        target = VAULT / "notes" / project
    else:
        target = VAULT / "inbox"

    path = write_memory(id, content, fm, target)
    return {
        "accepted": score >= threshold,
        "score": score,
        "id": id,
        "path": str(path),
        "reason": f"score={score} ({'accepted' if score >= threshold else 'below threshold ' + str(threshold)})"
    }

def recall(query=None, project=None, type=None, tags=None,
           min_score=0, status="active", limit=10) -> list:
    """检索记忆"""
    memories = read_all_memories(status)

    results = []
    for m in memories:
        fm = m["fm"]

        # 过滤
        if project and fm.get("project") != project: continue
        if type and fm.get("type") != type: continue
        if fm.get("score", 0) < min_score: continue
        if tags:
            mem_tags = set(fm.get("tags", []))
            if not set(tags) & mem_tags: continue

        # query 关键词匹配
        if query:
            q_lower = query.lower()
            body_lower = m["body"].lower()
            if q_lower not in body_lower and q_lower not in fm.get("title", "").lower():
                continue

        results.append({
            "id": fm.get("id"),
            "title": fm.get("title"),
            "snippet": m["body"][:200],
            "score": fm.get("score"),
            "type": fm.get("type"),
            "project": fm.get("project"),
            "tags": fm.get("tags"),
            "created_at": fm.get("created_at"),
            "status": fm.get("status"),
        })

    results.sort(key=lambda x: x["score"] or 0, reverse=True)
    return results[:limit]

def feedback(id: str, used: bool, helpful: bool = None):
    """agent 反馈"""
    for root in [VAULT/"notes", VAULT/"inbox"]:
        if not root.exists(): continue
        for path in root.rglob(f"{id}.md"):
            fm, body = parse_frontmatter(path.read_text())
            fm["hit_count"] = fm.get("hit_count", 0) + 1
            if used:
                fm["score"] = round(fm.get("score", 0) + 0.05, 1)
            if helpful:
                fm["score"] = round(fm.get("score", 0) + 0.1, 1)
            with open(path, "w") as f:
                f.write(f"---\n{yaml.dump(fm)}---\n{body}")
            return {"id": id, "hit_count": fm["hit_count"], "score": fm["score"]}
    return {"error": "not found"}

def cleanup():
    """过期归档"""
    today = datetime.now().strftime("%Y-%m-%d")
    count = 0
    for root in [VAULT/"notes", VAULT/"inbox"]:
        if not root.exists(): continue
        for path in root.rglob("*.md"):
            fm, body = parse_frontmatter(path.read_text())
            if fm.get("status") != "active": continue
            if fm.get("expires_at", "9999") < today:
                fm["status"] = "archived"
                archive_dir = VAULT/"archive"/fm.get("project", "unknown")
                archive_dir.mkdir(parents=True, exist_ok=True)
                new_path = archive_dir / path.name
                with open(new_path, "w") as f:
                    f.write(f"---\n{yaml.dump(fm)}---\n{body}")
                path.unlink()
                count += 1
    return {"archived": count}
