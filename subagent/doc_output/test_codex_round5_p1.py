"""Regression tests for codex review round-5 P1.

The pre-fix `_replace_variables` used a catch-all regex
``re.sub(r'\\{\\{[^}]+\\}\\}', '', template)`` to strip every unmatched
placeholder. This deleted both:
  - {{content_source_N}} slots (TEMPLATES.md §1-§7 use these as source labels)
  - {{include_diagram:D-XXX}} slots (TEMPLATES.md §0.4 placeholders)

So any doc_type that successfully loaded TEMPLATES.md produced an empty-skeleton
output. After the fix:
  - {{content_source_N}} → short source-type label like "代码仓库分析"
  - {{include_diagram:D-XXX}} → visible mermaid stub with TODO comment
  - Truly unknown placeholders still stripped (catch-all preserved as last step)

Run with: python3 -m unittest test_codex_round5_p1.py
"""

from __future__ import annotations

import unittest

from template_engine import TemplateEngine


class TestContentSourceLabelSubstitution(unittest.TestCase):
    def test_content_source_with_code_repository_label(self):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={
                "overview": "",
                "details": {
                    "repo": {"type": "code_repository", "content": "x"},
                },
                "references": [],
            },
            options={},
        )
        engine._load_template = lambda: "源: {{content_source_1}}\n"
        out = engine.render()
        self.assertIn("代码仓库分析", out)
        self.assertNotIn("{{content_source_1}}", out)

    def test_content_source_with_feishu_doc_label(self):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={
                "overview": "",
                "details": {
                    "doc": {"type": "feishu_doc", "content": "x"},
                },
                "references": [],
            },
            options={},
        )
        engine._load_template = lambda: "源: {{content_source_1}}\n"
        out = engine.render()
        self.assertIn("飞书文档", out)

    def test_content_source_with_no_details_falls_back_to_pending_label(self):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={"overview": "", "details": {}, "references": []},
            options={},
        )
        engine._load_template = lambda: "源: {{content_source_1}}\n"
        out = engine.render()
        self.assertIn("待标注", out)

    def test_unnumbered_content_source_also_filled(self):
        """TEMPLATES.md §3 research_report uses {{content_source}} (no number suffix)
        repeated across sections. Regex must match both forms."""
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={
                "overview": "",
                "details": {"r": {"type": "feishu_doc", "content": "x"}},
                "references": [],
            },
            options={},
        )
        engine._load_template = lambda: (
            "源1: {{content_source}}\n"
            "源2: {{content_source_2}}\n"
            "源3: {{content_source}}\n"
        )
        out = engine.render()
        # All 3 slots filled with the same label
        self.assertEqual(out.count("飞书文档"), 3)
        self.assertNotIn("{{content_source", out)

    def test_multiple_content_source_slots_all_filled(self):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={
                "overview": "",
                "details": {"r": {"type": "code_repository", "content": "x"}},
                "references": [],
            },
            options={},
        )
        engine._load_template = lambda: (
            "源1: {{content_source_1}}\n"
            "源2: {{content_source_2}}\n"
            "源3: {{content_source_5}}\n"
        )
        out = engine.render()
        self.assertEqual(out.count("代码仓库分析"), 3)
        for slot in ("{{content_source_1}}", "{{content_source_2}}", "{{content_source_5}}"):
            self.assertNotIn(slot, out)


class TestDiagramStubSubstitution(unittest.TestCase):
    def test_include_diagram_d_arch_replaced_with_stub(self):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={"overview": "", "details": {}, "references": []},
            options={},
        )
        engine._load_template = lambda: "## 架构\n\n{{include_diagram:D-ARCH}}\n"
        out = engine.render()
        # Placeholder stub appears, not just nothing
        self.assertIn("<!-- TODO: 补 D-ARCH mermaid 图", out)
        self.assertIn("```mermaid", out)
        self.assertIn("PLACEHOLDER D-ARCH", out)
        # Original placeholder gone
        self.assertNotIn("{{include_diagram:D-ARCH}}", out)

    def test_include_diagram_multiple_codes_all_substituted(self):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={"overview": "", "details": {}, "references": []},
            options={},
        )
        engine._load_template = lambda: (
            "{{include_diagram:D-ARCH}}\n"
            "{{include_diagram:D-SEQ}}\n"
            "{{include_diagram:D-CLASS}}\n"
        )
        out = engine.render()
        self.assertIn("D-ARCH", out)
        self.assertIn("D-SEQ", out)
        self.assertIn("D-CLASS", out)
        self.assertEqual(out.count("```mermaid"), 3)


class TestUnknownPlaceholdersStillStripped(unittest.TestCase):
    def test_random_placeholders_still_stripped(self):
        engine = TemplateEngine(
            doc_type="design_doc",
            merged_content={"overview": "", "details": {}, "references": []},
            options={},
        )
        engine._load_template = lambda: "literal {{unknown_var}} marker\n"
        out = engine.render()
        self.assertNotIn("{{unknown_var}}", out)
        self.assertIn("literal  marker", out)


class TestNoRegressionOnPreviousRichSkeletonPath(unittest.TestCase):
    """The rich-skeleton fallback path (R1-D2) still works — its
    {{include_diagram:D-XXX}} placeholders now produce visible stubs."""

    def test_rich_skeleton_renders_with_diagram_stubs(self):
        engine = TemplateEngine(
            doc_type="tech_review",
            merged_content={"overview": "OV", "details": {}, "references": []},
            options={},
        )
        engine._load_template = engine._get_rich_skeleton_template
        out = engine.render()
        # OV substituted
        self.assertIn("OV", out)
        # Each diagram slot is now a visible TODO stub
        for code in ("D-ARCH", "D-SEQ", "D-DAG", "D-CLASS"):
            self.assertIn(f"补 {code} mermaid", out)


if __name__ == "__main__":
    unittest.main()
