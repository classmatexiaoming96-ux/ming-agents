"""Tests for template_engine mermaid auto-annotation (v2.4).

Covers _classify_mermaid_body heuristic + annotate_mermaid_blocks idempotency +
TemplateEngine.render() opt-out via options["annotate_mermaid"]=False.

Run with: python3 -m unittest test_template_engine_annotate.py
"""

from __future__ import annotations

import unittest

from template_engine import (
    _classify_mermaid_body,
    annotate_mermaid_blocks,
    TemplateEngine,
)


class TestClassifier(unittest.TestCase):
    def test_sequence_diagram(self):
        body = "sequenceDiagram\n  A->>B: ping\n"
        self.assertEqual(_classify_mermaid_body(body), "D-SEQ")

    def test_class_diagram(self):
        self.assertEqual(_classify_mermaid_body("classDiagram\n  class Foo\n"), "D-CLASS")

    def test_er_diagram_classified_as_d_class(self):
        # ER 图与类图同档, alias 到 D-CLASS
        self.assertEqual(_classify_mermaid_body("erDiagram\n  USER ||--o{ POST : writes\n"), "D-CLASS")

    def test_state_diagram(self):
        self.assertEqual(_classify_mermaid_body("stateDiagram-v2\n  [*] --> S1\n"), "D-STATE")

    def test_mindmap(self):
        self.assertEqual(_classify_mermaid_body("mindmap\n  root\n    A\n"), "D-MIND")

    def test_gantt(self):
        self.assertEqual(_classify_mermaid_body("gantt\n  title X\n  section A\n"), "D-GANTT")

    def test_flowchart_with_phase_subgraph_is_dag(self):
        body = (
            "flowchart LR\n"
            "  subgraph Phase1[\"Phase 1\"]\n"
            "    A --> B\n"
            "  end\n"
        )
        self.assertEqual(_classify_mermaid_body(body), "D-DAG")

    def test_flowchart_with_generic_subgraph_is_arch(self):
        body = (
            "flowchart TB\n"
            "  subgraph Client[\"接入层\"]\n"
            "    A1\n"
            "  end\n"
        )
        self.assertEqual(_classify_mermaid_body(body), "D-ARCH")

    def test_flowchart_no_subgraph_is_decision(self):
        body = "flowchart TD\n  A{x?} -->|y| B\n  A -->|n| C\n"
        self.assertEqual(_classify_mermaid_body(body), "D-DECISION")

    def test_graph_keyword_treated_like_flowchart(self):
        # Mermaid `graph LR ...` is a flowchart alias
        body = "graph LR\n  subgraph Layer\n    A\n  end\n"
        self.assertEqual(_classify_mermaid_body(body), "D-ARCH")

    def test_unknown_syntax_returns_none(self):
        # journey, pie, requirementDiagram etc. — leave un-classified
        self.assertIsNone(_classify_mermaid_body("journey\n  title X\n"))
        self.assertIsNone(_classify_mermaid_body("pie\n  \"A\" : 50\n"))


class TestAnnotateMermaidBlocks(unittest.TestCase):
    def test_annotates_sequence_block(self):
        md = "## §3\n\n```mermaid\nsequenceDiagram\n  A->>B: x\n```\n"
        out = annotate_mermaid_blocks(md)
        self.assertIn("<!-- chart: D-SEQ -->", out)
        # Annotation appears immediately before fence
        self.assertIn("<!-- chart: D-SEQ -->\n```mermaid", out)

    def test_annotates_multiple_blocks_with_correct_codes(self):
        md = (
            "# Doc\n\n"
            "```mermaid\nflowchart TB\n  subgraph Client\n    A\n  end\n```\n\n"
            "```mermaid\nsequenceDiagram\n  A->>B: x\n```\n\n"
            "```mermaid\nclassDiagram\n  class Foo\n```\n"
        )
        out = annotate_mermaid_blocks(md)
        # Order preserved, each block annotated correctly
        d_arch_idx = out.index("<!-- chart: D-ARCH -->")
        d_seq_idx = out.index("<!-- chart: D-SEQ -->")
        d_class_idx = out.index("<!-- chart: D-CLASS -->")
        self.assertLess(d_arch_idx, d_seq_idx)
        self.assertLess(d_seq_idx, d_class_idx)

    def test_idempotent_when_block_already_annotated(self):
        md = (
            "## §3\n\n"
            "<!-- chart: D-SEQ -->\n"
            "```mermaid\nsequenceDiagram\n  A->>B: x\n```\n"
        )
        out = annotate_mermaid_blocks(md)
        # No double annotation
        self.assertEqual(out.count("<!-- chart: D-SEQ -->"), 1)
        # Running it again is also a no-op (idempotency)
        self.assertEqual(annotate_mermaid_blocks(out), out)

    def test_skips_blocks_with_unknown_syntax(self):
        md = "```mermaid\njourney\n  title X\n  section A\n```\n"
        out = annotate_mermaid_blocks(md)
        self.assertNotIn("<!-- chart:", out)

    def test_no_mermaid_blocks_means_no_change(self):
        md = "# Plain doc\n\nSome ```python\nprint('hi')\n``` code, no mermaid.\n"
        self.assertEqual(annotate_mermaid_blocks(md), md)

    def test_preserves_mermaid_body_byte_for_byte(self):
        md = "```mermaid\nflowchart TD\n  A --> B\n```\n"
        out = annotate_mermaid_blocks(md)
        # The body content survives untouched
        self.assertIn("flowchart TD\n  A --> B", out)


class TestTemplateEngineRender(unittest.TestCase):
    """Verify the engine's render() hook calls annotate_mermaid_blocks when enabled.

    Pre-existing bug note: TemplateEngine._get_default_template() uses ``{{name}}``
    inside an f-string which collapses to ``{name}``, so ``_replace_variables`` then
    fails to substitute (it matches ``{{name}}``). Fixing that bug is out of scope
    for this commit; the tests patch ``_load_template`` to inject content directly.
    """

    _SAMPLE_TEMPLATE_WITH_ARCH = (
        "# Doc\n\n"
        "## §2 架构\n\n"
        "```mermaid\n"
        "flowchart TB\n"
        "  subgraph Client\n    A\n  end\n"
        "```\n"
    )

    def _make_engine(self, options):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={"overview": "", "details": {}, "references": []},
            options=options,
        )
        engine._load_template = lambda: self._SAMPLE_TEMPLATE_WITH_ARCH
        return engine

    def test_render_annotates_mermaid_by_default(self):
        out = self._make_engine(options={}).render()
        self.assertIn("<!-- chart: D-ARCH -->", out)
        # mermaid body survives
        self.assertIn("flowchart TB", out)

    def test_render_skips_annotation_when_disabled(self):
        out = self._make_engine(options={"annotate_mermaid": False}).render()
        self.assertNotIn("<!-- chart:", out)
        self.assertIn("flowchart TB", out)


if __name__ == "__main__":
    unittest.main()
