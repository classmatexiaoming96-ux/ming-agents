"""Regression tests for v2.6 Phase 3 — code-review findings.

Covers:
  P0-1: _extract_template_section must strip the inner ```markdown fence so
        diagram placeholders land in a real document body (not nested inside
        a literal code fence whose ``` gets prematurely closed by the first
        inner ```mermaid stub).
  P1-1: DocOutput exposes a public re-check API so direct callers can verify
        unfilled_diagram_count == 0 after they fill in mermaid.
  P1-2: module_plan and research_report fallback skeletons must be D-DAG-only,
        matching the contract in doc-output-mermaid-prompt.md.
  P2  : - generator/skill version reads 2.6.0
        - {{include_diagram:multi}} expands to two stubs (D-ARCH + D-SEQ)
        - bare {{include_diagram}} expands to a GENERIC stub (no silent drop)
        - new namespaced sentinel %% DOC_OUTPUT_DIAGRAM_PLACEHOLDER is used
        - legacy %% PLACEHOLDER sentinel still counts for backward compat

Run with: python3 -m pytest test_phase3_review_fixes.py -v
"""

from __future__ import annotations

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from template_engine import TemplateEngine
from doc_output import DocOutput


# ---------------------------------------------------------------------------
# P0-1
# ---------------------------------------------------------------------------

class TestMarkdownFenceUnwrap(unittest.TestCase):
    """P0-1: the real TEMPLATES.md path must not leak ```markdown wrapper."""

    def _render(self, doc_type: str) -> str:
        engine = TemplateEngine(
            doc_type=doc_type,
            merged_content={'overview': 'OV', 'details': {}, 'references': []},
            options={},
        )
        return engine.render()

    def test_rendered_doc_has_no_markdown_fence_wrapper(self):
        for dt in (
            'tech_review', 'design_doc', 'research_report',
            'module_plan', 'task_plan', 'test_plan',
        ):
            with self.subTest(doc_type=dt):
                out = self._render(dt)
                # 关键不变量: 渲染产物不应再含 ``` ```markdown ``` 围栏 ——
                # 那是 TEMPLATES.md 用来包模板的语法糖,不该出现在产物里。
                self.assertNotIn(
                    '```markdown', out,
                    f'{dt} render still contains the ```markdown wrapper',
                )

    def test_rendered_doc_does_not_leak_section_metadata(self):
        for dt in ('tech_review', 'design_doc', 'module_plan'):
            with self.subTest(doc_type=dt):
                out = self._render(dt)
                self.assertNotIn(
                    '### 适用场景', out,
                    f'{dt} render leaked the §适用场景 metadata wrapper',
                )
                self.assertNotIn('### 模板结构', out)

    def test_rendered_doc_starts_with_document_title(self):
        out = self._render('tech_review')
        # First non-empty content line should be the actual H1 doc title
        first_line = next(l for l in out.splitlines() if l.strip())
        self.assertEqual(first_line.strip(), '# 技术评审文档')

    def test_diagram_stubs_are_balanced_fences(self):
        """The stub itself must be a self-contained ```mermaid ... ``` block —
        not nested inside (and breaking) an outer ```markdown fence."""
        out = self._render('tech_review')
        # Each ```mermaid opens; the next ``` closes — count must be even.
        n_mermaid_open = out.count('```mermaid')
        # Total ``` fences = 2 × n_mermaid_open (no other code blocks in tech_review template)
        n_total_fence = out.count('```')
        self.assertEqual(
            n_total_fence, 2 * n_mermaid_open,
            'mermaid fence count is not balanced — outer wrapper likely leaking',
        )

    def test_legacy_unfenced_section_still_returned_whole(self):
        """Backward compat: if a section has no ```markdown fence (legacy form),
        the loader returns the section body so existing templates keep working."""
        legacy_md = (
            "## tech_review（技术评审文档）\n\n"
            "Legacy un-fenced template body with {{include_diagram:D-ARCH}}\n"
        )
        with tempfile.TemporaryDirectory() as tmp:
            templates_path = Path(tmp) / 'TEMPLATES.md'
            templates_path.write_text(legacy_md, encoding='utf-8')
            engine = TemplateEngine(
                doc_type='tech_review',
                merged_content={'overview': '', 'details': {}, 'references': []},
                options={},
            )
            with patch.object(TemplateEngine, 'TEMPLATE_DIR', tmp):
                tpl = engine._load_template()
        # Un-fenced templates fall back to whole-section return.
        self.assertIn('Legacy un-fenced template body', tpl)
        self.assertIn('{{include_diagram:D-ARCH}}', tpl)


# ---------------------------------------------------------------------------
# P1-1
# ---------------------------------------------------------------------------

class TestRecheckUnfilledDiagrams(unittest.TestCase):
    """P1-1: callers can re-check the count on a filled-in document."""

    def test_count_unfilled_diagrams_on_empty_returns_zero(self):
        self.assertEqual(DocOutput.count_unfilled_diagrams(''), 0)
        self.assertEqual(DocOutput.count_unfilled_diagrams(None), 0)

    def test_count_on_fresh_doc_matches_render_count(self):
        engine = TemplateEngine(
            'tech_review',
            {'overview': '', 'details': {}, 'references': []},
            {},
        )
        out = engine.render()
        self.assertEqual(DocOutput.count_unfilled_diagrams(out), 3)

    def test_count_goes_to_zero_after_filling(self):
        engine = TemplateEngine(
            'tech_review',
            {'overview': '', 'details': {}, 'references': []},
            {},
        )
        out = engine.render()
        sentinel = DocOutput.DIAGRAM_PLACEHOLDER_SENTINEL
        # Caller would replace each sentinel line with real mermaid; here we
        # just drop the sentinel lines to simulate "filled".
        filled = '\n'.join(
            line for line in out.splitlines() if sentinel not in line
        )
        self.assertEqual(DocOutput.count_unfilled_diagrams(filled), 0)

    def test_recheck_from_file_path(self):
        engine = TemplateEngine(
            'design_doc',
            {'overview': '', 'details': {}, 'references': []},
            {},
        )
        out = engine.render()
        with tempfile.TemporaryDirectory() as tmp:
            p = os.path.join(tmp, 'doc.md')
            with open(p, 'w', encoding='utf-8') as f:
                f.write(out)
            self.assertEqual(DocOutput.recheck_unfilled_diagrams(p), 2)
            # Now simulate filling: rewrite the file without sentinels
            sentinel = DocOutput.DIAGRAM_PLACEHOLDER_SENTINEL
            filled = '\n'.join(
                line for line in out.splitlines() if sentinel not in line
            )
            with open(p, 'w', encoding='utf-8') as f:
                f.write(filled)
            self.assertEqual(DocOutput.recheck_unfilled_diagrams(p), 0)

    def test_legacy_sentinel_still_recognized(self):
        """A v2.6 Phase 2 document (old sentinel %% PLACEHOLDER) should still
        produce a non-zero count, even though new docs use the namespaced one."""
        legacy_doc = (
            "## Section\n\n"
            "```mermaid\n%% PLACEHOLDER D-ARCH — fill me\n```\n"
        )
        self.assertEqual(DocOutput.count_unfilled_diagrams(legacy_doc), 1)


# ---------------------------------------------------------------------------
# P1-2
# ---------------------------------------------------------------------------

class TestPlanningFallbackContractAlignment(unittest.TestCase):
    """P1-2: module_plan/research_report fallback skeletons are D-DAG-only."""

    def _fallback_template(self, doc_type: str) -> str:
        engine = TemplateEngine(
            doc_type=doc_type,
            merged_content={'overview': '', 'details': {}, 'references': []},
            options={},
        )
        # Force TEMPLATES.md miss → fallback path
        with tempfile.TemporaryDirectory() as tmp:
            with patch.object(TemplateEngine, 'TEMPLATE_DIR', tmp):
                return engine._load_template()

    def test_module_plan_fallback_is_d_dag_only(self):
        tpl = self._fallback_template('module_plan')
        self.assertIn('D-DAG', tpl)
        # Contract: NOT D-ARCH/D-SEQ/D-CLASS for module_plan
        self.assertNotIn('D-ARCH', tpl)
        self.assertNotIn('D-SEQ', tpl)
        self.assertNotIn('D-CLASS', tpl)

    def test_research_report_fallback_is_d_dag_only(self):
        tpl = self._fallback_template('research_report')
        self.assertIn('D-DAG', tpl)
        self.assertNotIn('D-ARCH', tpl)
        self.assertNotIn('D-SEQ', tpl)
        self.assertNotIn('D-CLASS', tpl)

    def test_task_plan_fallback_still_d_dag_only(self):
        tpl = self._fallback_template('task_plan')
        self.assertIn('D-DAG', tpl)
        self.assertNotIn('D-ARCH', tpl)

    def test_test_plan_fallback_still_d_dag_only(self):
        tpl = self._fallback_template('test_plan')
        self.assertIn('D-DAG', tpl)
        self.assertNotIn('D-ARCH', tpl)

    def test_tech_review_fallback_keeps_full_figure_set(self):
        """Regression: tech_review/design_doc must still get the rich set."""
        tpl = self._fallback_template('tech_review')
        for code in ('D-ARCH', 'D-SEQ', 'D-DAG', 'D-CLASS'):
            self.assertIn(code, tpl, f'tech_review missing {code}')

    def test_per_type_section_title_used_in_fallback(self):
        """The fallback skeleton uses the per-doc-type DAG caption."""
        tpl_module = self._fallback_template('module_plan')
        self.assertIn('模块依赖关系', tpl_module)
        tpl_research = self._fallback_template('research_report')
        self.assertIn('推荐方案实施路径', tpl_research)


# ---------------------------------------------------------------------------
# P2
# ---------------------------------------------------------------------------

class TestPlaceholderVariantsExpanded(unittest.TestCase):
    """P2: {{include_diagram:multi}} and bare {{include_diagram}} are expanded,
    not silently stripped by the catch-all."""

    def _render_with_template(self, template: str) -> str:
        engine = TemplateEngine(
            'design_doc',
            {'overview': '', 'details': {}, 'references': []},
            {},
        )
        engine._load_template = lambda: template
        return engine.render()

    def test_multi_variant_expands_to_two_stubs(self):
        out = self._render_with_template(
            "## Section\n\n{{include_diagram:multi}}\n"
        )
        self.assertIn('D-ARCH', out)
        self.assertIn('D-SEQ', out)
        # Two mermaid stubs
        self.assertEqual(out.count('```mermaid'), 2)
        self.assertEqual(DocOutput.count_unfilled_diagrams(out), 2)
        self.assertNotIn('{{include_diagram:multi}}', out)

    def test_bare_variant_expands_to_generic_stub(self):
        out = self._render_with_template(
            "## Section\n\n{{include_diagram}}\n"
        )
        # GENERIC code in the stub
        self.assertIn('GENERIC', out)
        self.assertEqual(out.count('```mermaid'), 1)
        self.assertEqual(DocOutput.count_unfilled_diagrams(out), 1)
        self.assertNotIn('{{include_diagram}}', out)

    def test_typed_variant_still_works(self):
        out = self._render_with_template(
            "## Section\n\n{{include_diagram:D-ARCH}}\n"
        )
        self.assertIn('D-ARCH', out)
        self.assertEqual(DocOutput.count_unfilled_diagrams(out), 1)

    def test_unknown_variant_form_still_stripped(self):
        """Anything not matching the three known forms still falls to
        the catch-all (preserves backward compat for genuinely unknown vars)."""
        out = self._render_with_template(
            "literal {{totally_unknown}} marker\n"
        )
        self.assertNotIn('{{totally_unknown}}', out)


class TestSentinelNamespacing(unittest.TestCase):
    """P2: new sentinel uses the doc_output namespace to avoid collisions."""

    def test_new_sentinel_has_namespace_prefix(self):
        self.assertTrue(
            DocOutput.DIAGRAM_PLACEHOLDER_SENTINEL.startswith('%% DOC_OUTPUT_'),
            'sentinel should be namespaced to doc_output',
        )

    def test_render_uses_new_sentinel(self):
        engine = TemplateEngine(
            'design_doc',
            {'overview': '', 'details': {}, 'references': []},
            {},
        )
        engine._load_template = lambda: "{{include_diagram:D-ARCH}}\n"
        out = engine.render()
        self.assertIn('%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER D-ARCH', out)
        # The old generic "%% PLACEHOLDER D-ARCH" form should NOT appear in
        # fresh renders — but a substring "PLACEHOLDER D-ARCH" will, because
        # it's part of the longer namespaced sentinel. Check the full new form.

    def test_doc_with_real_placeholder_word_not_counted(self):
        """A document that legitimately contains the word 'PLACEHOLDER' in
        prose (e.g. discussing the concept) must NOT be miscounted as long
        as the new namespaced sentinel is what fresh docs emit."""
        doc_with_prose_placeholder = (
            "# Section\n\n"
            "Use the PLACEHOLDER convention to mark TODO items.\n"
            "But this is just prose, not a real diagram stub.\n\n"
            "```mermaid\n"
            "%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER D-ARCH — fill me\n"
            "```\n"
        )
        # Only 1 real placeholder (the new sentinel); the prose mention is ignored.
        self.assertEqual(
            DocOutput.count_unfilled_diagrams(doc_with_prose_placeholder), 1,
        )


class TestVersionSync(unittest.TestCase):
    """P2: returned version field matches the announced v2.6 line."""

    def test_generate_output_returns_2_6_version(self):
        cfg = {'doc_type': 'tech_review', 'workspace': '/tmp', 'options': {}}
        d = DocOutput(cfg)
        # populate state so _generate_output doesn't crash
        d.rendered_doc = '# x'
        d.merged_content = {'overview': '', 'details': {}, 'references': []}
        d.local_path = '/tmp/x.md'
        d.feishu_url = ''
        result = d._generate_output()
        self.assertEqual(result['version'], '2.6.0')


# ---------------------------------------------------------------------------
# End-to-end smoke
# ---------------------------------------------------------------------------

class TestEndToEndChain(unittest.TestCase):
    """Smoke test: render → fill → recheck for the primary chain (review point 6)."""

    def test_tech_review_full_round_trip(self):
        engine = TemplateEngine(
            'tech_review',
            {'overview': 'OV', 'details': {}, 'references': []},
            {},
        )
        out = engine.render()
        # Chain assertion 1: produces a valid-looking document (no broken wrapper)
        self.assertNotIn('```markdown', out)
        self.assertTrue(out.lstrip().startswith('# 技术评审文档'))
        # Chain assertion 2: 3 placeholders (D-ARCH, D-SEQ, D-CLASS)
        initial_count = DocOutput.count_unfilled_diagrams(out)
        self.assertEqual(initial_count, 3)
        # Chain assertion 3: caller can fill and re-check to 0
        sentinel = DocOutput.DIAGRAM_PLACEHOLDER_SENTINEL
        filled = '\n'.join(
            line for line in out.splitlines() if sentinel not in line
        )
        self.assertEqual(DocOutput.count_unfilled_diagrams(filled), 0)


if __name__ == '__main__':
    unittest.main()
