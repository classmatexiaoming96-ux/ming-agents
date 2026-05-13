#!/usr/bin/env python3
"""
doc-output 主执行脚本

用法:
    python doc_output.py --config config.yaml
    python doc_output.py --doc-type tech_review --input-sources code_repository --repos signal,signal_access

作者: Shrimp Team
版本: 2.2.0
"""

import argparse
import json
import re
import sys
import os
from datetime import datetime
from pathlib import Path

# 添加当前目录到 Python 路径
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from parallel_fetch import ParallelFetcher
from content_merger import ContentMerger
from template_engine import TemplateEngine
from feishu_publisher import FeishuPublisher


# ---------------------------------------------------------------------------
# IdeaRefiner: 将模糊想法结构化为 核心目标 / 约束条件 / 开放问题 三元组
# ---------------------------------------------------------------------------

class IdeaRefiner:
    """
    idea-refine 核心类。

    触发条件（满足任一）：
    1. doc_type == 'idea_refine'
    2. Orchestrator 标记了 requirement_type == 'rough_idea'
    3. 无明确输入源（input_sources 为空或所有源均无实质内容）AND 字数 < 200
    4. 含模糊词汇 AND 字数 < 200
    """

    # 模糊词汇列表
    VAGUE_WORDS = [
        "大概", "想做", "看看能不能", "初步想法", "可能要",
        "考虑一下", "探索", "试试", "能不能", "可行吗",
        "是不是可以", "也许", "可能的话", "简单做个"
    ]

    def __init__(self, raw_content: str = "", doc_type: str = "",
                 input_sources: list = None,
                 orchestrator_mark: dict = None,
                 workspace: str = "/root/.openclaw/workspace"):
        self.raw_content = raw_content or ""
        self.doc_type = doc_type
        self.input_sources = input_sources or []
        self.orchestrator_mark = orchestrator_mark or {}
        self.workspace = workspace
        self.idea_refinement_md = ""

    def is_idea_refine_needed(self) -> bool:
        """判断是否需要执行 idea-refine"""
        # 条件1: 显式指定 doc_type
        if self.doc_type == "idea_refine":
            return True

        # 条件2: Orchestrator 标记
        req_type = self.orchestrator_mark.get("requirement_type", "")
        if req_type == "rough_idea":
            return True

        # 条件3+4: 字数 + 模糊词检测
        content = self.raw_content.strip()
        word_count = len(content)

        if word_count == 0:
            # 无 raw_content 时,仅在没有任何 input_sources 的情况下才进 idea-refine。
            # 注意: 通过 --input-sources code_repository --repos ... 等命令行调用
            # 通常没有显式 raw_content,但 input_sources 非空,不应被强行送入 idea-refine。
            if not self.input_sources:
                return True
            # 有 input_sources -> 跳过 idea-refine, 走正常 fetch/render/publish 路径
            return False

        if word_count < 200:
            # 条件3: 无明确输入源 — 同时检查 raw_content 内含 URL/文件路径
            # 与外部结构化 input_sources。spec (doc-output-idearefin.md:13) 把
            # 这两者都算"明确输入源",任一存在就不应进 idea-refine。
            has_clear_source = bool(
                re.search(r'https?://', content) or
                re.search(r'/[\w\-./]+', content)
            )
            if not has_clear_source and not self.input_sources:
                return True

            # 条件4: 含模糊词汇
            if self._contains_vague_words(content):
                return True

        return False

    def _contains_vague_words(self, content: str) -> bool:
        """检查是否包含模糊词汇"""
        return any(word in content for word in self.VAGUE_WORDS)

    def refine(self) -> str:
        """
        执行结构化发散/收敛，生成 idea_refinement.md。

        产出格式：
        docs/idea_refinement_{timestamp}.md
        """
        docs_dir = os.path.join(self.workspace, "docs")
        os.makedirs(docs_dir, exist_ok=True)

        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        filename = f"idea_refinement_{timestamp}.md"
        output_path = os.path.join(docs_dir, filename)

        content = self._generate_refinement_content()
        self.idea_refinement_md = content

        with open(output_path, "w", encoding="utf-8") as f:
            f.write(content)

        return output_path

    def _generate_refinement_content(self) -> str:
        """
        生成 idea_refinement.md 正文。

        这里输出的是结构化框架 + LLM 思考区（由调用方/用户填充）。
        实际内容由 LLM 基于 self.raw_content 生成。
        """
        raw_snippet = self.raw_content.strip()[:500] if self.raw_content else "(无原始输入)"

        return f"""# Idea Refinement 结果

> 生成时间: {datetime.now().strftime("%Y-%m-%d %H:%M:%S")}
> 原始输入: {raw_snippet[:200]}...

---

## 核心目标

（收敛到 1-3 个核心目标，简化为一句话可陈述的意图）

**填写指引**：
- 每个目标必须是**可验证的**（有指标）
- 必须包含**受益方**（谁因此受益）
- 避免解决方案语言（不要说"用 WebSocket"）

示例：
> 核心目标：在告警产生后 1 分钟内，通过智能合并将同源/同类告警收敛为 1 条，
> 将日均告警量减少 70% 以上，同时保证 critical 告警零漏报。

**LLM 分析区（请基于原始输入填充）**：

```
[由 LLM 根据 "{raw_snippet[:100]}..." 分析填写]
```

---

## 约束条件

（收敛到明确的边界条件，包括：技术约束、时间约束、质量约束、资源约束）

**填写指引**：
- 约束必须是**硬边界**（不可突破）
- 区分"已确认约束"和"推测约束"（标注来源）

示例：
> - 技术约束：必须兼容 iOS 12+ 和 Android 8+
> - 性能约束：首屏加载时间 ≤ 2s
> - 资源约束：团队 2 人，2 个月内交付

**LLM 分析区（请基于原始输入填充）**：

```
[由 LLM 根据原始输入分析填写]
```

---

## 开放问题

（尚需探索的问题，按优先级排序）

**填写指引**：
- 每个开放问题必须附带**不确定性描述**
- P0 = 阻塞立项，P1 = 阻塞技术方案，P2 = 可后续决定

示例：
> 1. [P0] 收敛策略选择：基于规则的关键词匹配 vs 基于 ML 的语义聚类？
>    — 不确定：规则简单但需人工维护，ML 准确但冷启动难
> 2. [P0] "同类告警"的定义边界在哪里？
>    — 不确定：是按服务/模块维度，还是按错误类型维度

**LLM 分析区（请基于原始输入填充）**：

```
[由 LLM 根据原始输入分析填写]
```

---

## 收敛路径记录

（发散过程中产生的备选方向/方案，仅作记录，不深入展开）

**填写指引**：
- 记录已放弃的方向及其放弃原因
- 帮助后续追溯决策过程

示例：
> - 备选方向A（ML 智能收敛）：已放弃 → 原因：2 人团队无法在 2 个月内完成 ML 模型训练和调优
> - 备选方向B（完全自定义规则引擎）：已放弃 → 原因：规则配置复杂，与 MVP 快速交付目标冲突
> - 收敛到方向C（规则 + 轻量级聚类混合）：当前推荐

**LLM 分析区（请基于原始输入填充）**：

```
[由 LLM 记录发散-收敛过程]
```

---

## 原始输入

```markdown
{self.raw_content}
```
"""


# ---------------------------------------------------------------------------
# DocOutput 主类
# ---------------------------------------------------------------------------

class DocOutput:
    """doc-output 主类"""

    def __init__(self, config: dict):
        self.config = config
        self.doc_type = config.get("doc_type", "research_report")
        self.raw_content = config.get("raw_content", "")
        self.orchestrator_mark = config.get("orchestrator_mark", {})
        self.input_sources = config.get("input_sources", [])
        self.output_path = config.get("output_path")
        self.options = config.get("options", {})
        self.workspace = config.get("workspace", "/root/.openclaw/workspace")

        # 各阶段结果
        self.fetch_results = {}
        self.merged_content = {}
        self.rendered_doc = ""
        self.local_path = ""
        self.feishu_url = ""

        # IdeaRefiner 结果
        self.idea_refiner = None
        self.idea_refinement_path = ""

        # 耗时统计
        self.timing = {
            "idea_refine_seconds": 0,
            "parallel_fetch_seconds": 0,
            "merge_seconds": 0,
            "render_seconds": 0,
            "publish_seconds": 0,
            "total_seconds": 0
        }

    def run(self) -> dict:
        """执行 doc-output 流程"""
        start_time = datetime.now()

        print(f"[doc-output] 开始执行...")
        print(f"[doc-output] 文档类型: {self.doc_type}")

        # Step 0: Idea Refine 前置检查
        # — — — — — — — — — — — — — — — — — — — — — — — — —
        refiner = IdeaRefiner(
            raw_content=self.raw_content,
            doc_type=self.doc_type,
            input_sources=self.input_sources,
            orchestrator_mark=self.orchestrator_mark,
            workspace=self.workspace
        )

        if refiner.is_idea_refine_needed():
            print("[doc-output] ⚡ 检测到模糊需求，进入 idea-refine 流程")
            refine_start = datetime.now()

            self.idea_refiner = refiner
            self.idea_refinement_path = refiner.refine()
            self.timing["idea_refine_seconds"] = (
                datetime.now() - refine_start
            ).total_seconds()

            print(f"[doc-output] idea-refine 完成: {self.idea_refinement_path}")

            # 返回用户确认状态（由 Orchestrator 展示给用户）
            return self._generate_idea_refine_confirmation()

        print(f"[doc-output] Step 1: 解析请求 - 完成（无需 idea-refine）")

        # Step 2: 并行内容获取
        print(f"[doc-output] Step 2: 并行内容获取...")
        fetch_start = datetime.now()
        fetcher = ParallelFetcher(self.input_sources, self.workspace)
        self.fetch_results = fetcher.fetch_all()
        self.timing["parallel_fetch_seconds"] = (
            datetime.now() - fetch_start
        ).total_seconds()
        print(f"[doc-output] Step 2: 获取完成，共 {len(self.fetch_results)} 个结果")

        # Step 3: 内容融合
        print(f"[doc-output] Step 3: 内容融合...")
        merge_start = datetime.now()
        merger = ContentMerger(self.fetch_results)
        self.merged_content = merger.merge()
        self.timing["merge_seconds"] = (datetime.now() - merge_start).total_seconds()

        # 识别冲突项
        conflicts = merger.identify_conflicts()
        if conflicts:
            print(f"[doc-output] ⚠️ 发现 {len(conflicts)} 个冲突项，需要用户裁决")
            for conflict in conflicts:
                print(f"[doc-output]   - {conflict['dimension']}: "
                      f"{conflict['option_a']} vs {conflict['option_b']}")

        print(f"[doc-output] Step 3: 融合完成")

        # Step 4: 模板渲染
        print(f"[doc-output] Step 4: 模板渲染...")
        render_start = datetime.now()
        engine = TemplateEngine(self.doc_type, self.merged_content, self.options)
        self.rendered_doc = engine.render()
        self.timing["render_seconds"] = (datetime.now() - render_start).total_seconds()
        print(f"[doc-output] Step 4: 渲染完成")

        # Step 5: 保存文档
        print(f"[doc-output] Step 5: 保存文档...")
        self._save_document()
        print(f"[doc-output] Step 5: 保存完成: {self.local_path}")

        # Step 6: 发布飞书（可选, 含 D-ARCH/D-SEQ/D-DAG 自动升级画板, options.upgrade_charts 控制）
        if self.options.get("publish_feishu", True):
            print(f"[doc-output] Step 6: 发布飞书文档...")
            publish_start = datetime.now()
            try:
                self.feishu_url = self._publish_feishu(
                    title=self._generate_title(),
                    content=self.rendered_doc,
                )
                self.timing["publish_seconds"] = (
                    datetime.now() - publish_start
                ).total_seconds()
                print(f"[doc-output] Step 6: 发布完成: {self.feishu_url}")
            except Exception as e:
                print(f"[doc-output] ⚠️ 飞书发布失败: {e}")
                self.feishu_url = ""
        else:
            print("[doc-output] Step 6: 跳过飞书发布（publish_feishu=false）")

        # 总耗时
        self.timing["total_seconds"] = (datetime.now() - start_time).total_seconds()

        # Step 7: 输出结果
        result = self._generate_output()
        print(f"[doc-output] 执行完成！总耗时: {self.timing['total_seconds']:.2f}s")

        return result

    def _generate_idea_refine_confirmation(self) -> dict:
        """
        生成 idea-refine 阶段的用户确认输出。
        由 Orchestrator 接收后构造 askUserQuestion 发给用户。
        """
        return {
            "tag": "doc-output",
            "line": 1,
            "node": "idea-refine",
            "outputs": {
                "status": "pending_confirmation",
                "idea_refinement_path": self.idea_refinement_path,
                "summary": "已完成想法结构化，请确认方向是否正确",
                "confirmation_needed": True,
                "confirmation_context": {
                    "type": "idea_confirmation",
                    "question": (
                        "我对你的想法做了以下结构化分析，请确认方向是否正确："
                    ),
                    "context": (
                        "已生成 idea_refinement.md，"
                        "包含 核心目标 / 约束条件 / 开放问题 三元组"
                    ),
                    "options": [
                        {
                            "label": "A: 方向正确，继续展开",
                            "value": "confirm_continue"
                        },
                        {
                            "label": "B: 核心目标需要调整",
                            "value": "adjust_goal"
                        },
                        {
                            "label": "C: 补充约束条件",
                            "value": "add_constraints"
                        },
                        {
                            "label": "D: 其他澄清",
                            "value": "other_clarification"
                        }
                    ]
                },
                "timing": {
                    "idea_refine_seconds": self.timing["idea_refine_seconds"],
                    "total_seconds": self.timing["idea_refine_seconds"]
                },
                "error": None
            }
        }

    def continue_after_confirmation(self, user_choice: str) -> dict:
        """
        用户确认 idea-refine 后，继续执行后续流程。

        user_choice: confirm_continue / adjust_goal / add_constraints / other_clarification
        """
        if user_choice in ("adjust_goal", "add_constraints", "other_clarification"):
            # 用户需要调整，此时返回等待态，由 Orchestrator 重新调度
            return {
                "tag": "doc-output",
                "node": "idea-refine",
                "outputs": {
                    "status": "waiting_user_revision",
                    "message": (
                        f"用户选择了 {user_choice}，"
                        "请用户修改 idea_refinement.md 后重新传入"
                    )
                }
            }

        # user_choice == "confirm_continue": 继续执行并行获取
        print("[doc-output] ✅ 用户确认 idea-refine，继续执行并行获取...")

        fetch_start = datetime.now()
        fetcher = ParallelFetcher(self.input_sources, self.workspace)
        self.fetch_results = fetcher.fetch_all()
        self.timing["parallel_fetch_seconds"] = (
            datetime.now() - fetch_start
        ).total_seconds()

        merge_start = datetime.now()
        merger = ContentMerger(self.fetch_results)
        self.merged_content = merger.merge()
        self.timing["merge_seconds"] = (datetime.now() - merge_start).total_seconds()

        render_start = datetime.now()
        engine = TemplateEngine(self.doc_type, self.merged_content, self.options)
        self.rendered_doc = engine.render()
        self.timing["render_seconds"] = (datetime.now() - render_start).total_seconds()

        self._save_document()

        if self.options.get("publish_feishu", True):
            try:
                self.feishu_url = self._publish_feishu(
                    title=self._generate_title(),
                    content=self.rendered_doc,
                )
            except Exception:
                self.feishu_url = ""

        self.timing["total_seconds"] = (
            self.timing["idea_refine_seconds"] +
            self.timing["parallel_fetch_seconds"] +
            self.timing["merge_seconds"] +
            self.timing["render_seconds"]
        )

        return self._generate_output()

    def _publish_feishu(self, title: str, content: str) -> str:
        """发布到飞书,可选地把 D-ARCH/D-SEQ/D-DAG mermaid 块升级成画板。

        路由:
          1. options["upgrade_charts"] = False  → 走原 publisher.publish() (向后兼容)
          2. workspace_dir 不存在 / ChartPublisher 初始化失败 → fallback 到原 publish()
          3. 其它情况 → publisher.publish_with_charts() + ChartPublisher

        Returns:
            飞书文档 URL; 发布失败抛原 publisher 的 exception 由调用方处理。
        """
        publisher = FeishuPublisher()
        if not self.options.get("upgrade_charts", True):
            return publisher.publish(title=title, content=content)

        try:
            # 延迟导入避免在不需要画板升级时多花启动时间
            from chart_publisher import ChartPublisher, RealLarkCliClient
            ws = Path(self.workspace)
            if not ws.is_dir():
                print(f"[doc-output] ⚠️ workspace {ws} 不存在, 跳过画板升级走原 publish")
                return publisher.publish(title=title, content=content)
            charter = ChartPublisher(
                client=RealLarkCliClient(identity="bot"),
                workspace_dir=ws,
            )
        except Exception as e:
            print(f"[doc-output] ⚠️ ChartPublisher 初始化失败 ({e}), 走原 publish 不升级")
            return publisher.publish(title=title, content=content)

        result = publisher.publish_with_charts(
            title=title, content=content, chart_publisher=charter,
        )
        n_ok = len(result["chart_results"])
        n_skip = len(result["skipped"])
        if n_ok:
            print(f"[doc-output] ✅ 升级 {n_ok} 个画板:")
            for r in result["chart_results"]:
                mark = "✅" if r.roundtrip_ok else "⚠️ roundtrip 差异"
                print(f"    {mark} {r.chart_code} → {r.whiteboard_token}")
        if n_skip:
            print(f"[doc-output] ⚠️ {n_skip} 个 chart 跳过/失败:")
            for code, reason in result["skipped"]:
                print(f"    {code}: {reason}")
        return result["url"]

    def _generate_title(self) -> str:
        """生成文档标题"""
        titles = {
            "tech_review": "技术评审文档",
            "design_doc": "设计文档",
            "research_report": "调研报告",
            "module_plan": "模块拆分文档",
            "task_plan": "Task规划文档",
            "test_plan": "测试计划",
            "idea_refine": "想法结构化"
        }
        return titles.get(self.doc_type, "文档")

    def _save_document(self):
        """保存文档到本地"""
        if not self.output_path:
            timestamp = datetime.now().strftime("%Y%m%d_%H%M")
            filename = f"{self.doc_type}_{timestamp}.md"
            docs_dir = os.path.join(self.workspace, "docs")
            os.makedirs(docs_dir, exist_ok=True)
            self.output_path = os.path.join(docs_dir, filename)

        metadata = self._generate_metadata()
        full_content = metadata + "\n\n" + self.rendered_doc

        with open(self.output_path, "w", encoding="utf-8") as f:
            f.write(full_content)

        self.local_path = self.output_path

    def _generate_metadata(self) -> str:
        """生成元数据头"""
        input_sources_summary = []
        for source in self.input_sources:
            source_type = source.get("type", "unknown")
            if source_type == "code_repository":
                repos = source.get("repos", [])
                input_sources_summary.append({
                    "type": "code_repository",
                    "count": len(repos),
                    "parallel": True
                })
            elif source_type == "feishu_doc":
                docs = source.get("docs", [])
                input_sources_summary.append({
                    "type": "feishu_doc",
                    "count": len(docs),
                    "parallel": True
                })
            elif source_type == "web":
                urls = source.get("urls", [])
                searches = source.get("searches", [])
                input_sources_summary.append({
                    "type": "web",
                    "count": len(urls) + len(searches),
                    "parallel": True
                })
            elif source_type == "internal":
                tools = source.get("tools", [])
                input_sources_summary.append({
                    "type": "internal",
                    "count": len(tools),
                    "parallel": True
                })

        metadata = {
            "doc_type": self.doc_type,
            "version": "1.0",
            "status": "LOCKED",
            "generated_at": datetime.now().isoformat(),
            "input_sources": input_sources_summary,
            "generator": "shrimp-doc-output v2.2.0"
        }

        return "---\n" + json.dumps(metadata, indent=2, ensure_ascii=False) + "\n---"

    def _generate_output(self) -> dict:
        """生成输出结果"""
        return {
            "skill": "shrimp-doc-output",
            "version": "2.2.0",
            "status": "LOCKED",
            "doc_type": self.doc_type,
            "output_path": self.local_path,
            "feishu_url": self.feishu_url,
            "input_sources_used": [
                {"type": k, "count": v.get("count", 1), "parallel": True}
                for k, v in self.fetch_results.items()
            ],
            "timing": self.timing,
            "content_summary": (
                f"{self._generate_title()}已完成，"
                f"包含{len(self.merged_content)}个主要章节"
            ),
            "gate_conditions": {
                "content_collected": len(self.fetch_results) > 0,
                "template_rendered": bool(self.rendered_doc),
                "document_saved": bool(self.local_path),
                "user_confirmed": True
            }
        }


# ---------------------------------------------------------------------------
# 配置加载 & 命令行解析
# ---------------------------------------------------------------------------

def load_config(config_path: str) -> dict:
    """从 YAML 文件加载配置"""
    import yaml

    with open(config_path, "r", encoding="utf-8") as f:
        config = yaml.safe_load(f)

    return config


def parse_args():
    """解析命令行参数"""
    parser = argparse.ArgumentParser(
        description="doc-output: Shrimp 4.0 文档输出工具"
    )

    parser.add_argument("--config", "-c", help="配置文件路径 (YAML)")

    parser.add_argument(
        "--doc-type", "-t",
        default="research_report",
        choices=[
            "idea_refine", "tech_review", "design_doc",
            "research_report", "module_plan", "task_plan", "test_plan"
        ],
        help="文档类型"
    )

    parser.add_argument(
        "--input-sources", "-i",
        nargs="+",
        help="输入源类型列表 (code_repository, feishu_doc, web, internal, direct)"
    )

    parser.add_argument(
        "--repos", "-r",
        nargs="+",
        help="代码仓库路径列表"
    )

    parser.add_argument(
        "--feishu-docs", "-f",
        nargs="+",
        help="飞书文档 URL 列表"
    )

    parser.add_argument(
        "--raw-content",
        default="",
        help="原始输入内容（用于 idea-refine 判断）"
    )

    parser.add_argument(
        "--output", "-o",
        help="输出文件路径"
    )

    parser.add_argument(
        "--workspace", "-w",
        default="/root/.openclaw/workspace",
        help="工作空间目录"
    )

    parser.add_argument(
        "--no-feishu",
        action="store_true",
        help="不发布到飞书"
    )

    return parser.parse_args()


def build_config_from_args(args) -> dict:
    """从命令行参数构建配置"""
    config = {
        "doc_type": args.doc_type,
        "raw_content": args.raw_content,
        "input_sources": [],
        "output_path": args.output,
        "workspace": args.workspace,
        "orchestrator_mark": {},
        "options": {
            "publish_feishu": not args.no_feishu
        }
    }

    if args.input_sources:
        for source_type in args.input_sources:
            if source_type == "code_repository" and args.repos:
                config["input_sources"].append({
                    "type": "code_repository",
                    "repos": [{"path": r} for r in args.repos]
                })
            elif source_type == "feishu_doc" and args.feishu_docs:
                config["input_sources"].append({
                    "type": "feishu_doc",
                    "docs": [{"url": url} for url in args.feishu_docs]
                })
            elif source_type == "direct" and args.raw_content:
                config["input_sources"].append({
                    "type": "direct",
                    "content": args.raw_content,
                })

    return config


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def main():
    """主函数"""
    args = parse_args()

    if args.config:
        config = load_config(args.config)
    else:
        config = build_config_from_args(args)

    if "workspace" not in config:
        config["workspace"] = args.workspace

    doc_output = DocOutput(config)
    result = doc_output.run()

    print("\n" + "=" * 60)
    print("doc-output 执行结果")
    print("=" * 60)
    print(json.dumps(result, indent=2, ensure_ascii=False))

    return 0


if __name__ == "__main__":
    sys.exit(main())

# shrimp-doc-output v2.2.0
