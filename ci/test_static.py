#!/usr/bin/env python3
"""Regression tests for source discovery and static configuration contracts."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

import yaml
from source_files import ROOT, files, kind

SPEC = importlib.util.spec_from_file_location("formats", ROOT / "ci/analyze-formats.py")
FORMATS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FORMATS)


class StaticContracts(unittest.TestCase):
    def test_embedded_jq_awk_and_sed_programs_and_registered_extensions(self):
        import analyze
        samples = {'jq': "jq --arg tag value '.name == $tag' input.json", 'awk': "awk -F '|' '/x/ {print $1}' input", 'sed': "sed -e 's/a/b/g' -e 's/c/d/g' input"}
        for language, text in samples.items():
            programs = list(analyze.programs(text, language))
            self.assertEqual(len(programs), 2 if language == 'sed' else 1)
            for program in programs:
                analyze.check_program(program, language)
        with tempfile.TemporaryDirectory() as directory:
            for suffix in ['.awk', '.sed']:
                path = Path(directory)/('filter'+suffix)
                path.write_text('program')
                self.assertEqual(kind(path), suffix[1:])


    def test_recursive_inventory_includes_nested_fixtures_and_skips_vendor(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ["ci/tests/fixtures/example.sh", "build/vendor/third.go", "build/cmd/example.go"]:
                path = root / name;path.parent.mkdir(parents=True, exist_ok=True);path.write_text("text")
            self.assertEqual({str(path.relative_to(root)) for path in files(root)},
                             {"ci/tests/fixtures/example.sh", "build/cmd/example.go"})

    def test_new_unregistered_languages_fail_instead_of_disappearing(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "program.rs";path.write_text("fn main() {}")
            with self.assertRaisesRegex(ValueError, "No static validator"):kind(path)

    def test_strict_json_rejects_duplicates_nonfinite_and_truncation(self):
        for text in ['{"a":1,"a":2}', '{"value":NaN}', '{"value":Infinity}', '{"a":']:
            with self.subTest(text=text), self.assertRaises(ValueError):FORMATS.json_data(text)
        self.assertEqual(FORMATS.json_data('{"zero":0,"empty":null}'), {"zero":0,"empty":None})

    def test_yaml_on_is_a_string_and_explicit_duplicate_keys_fail(self):
        value = yaml.load("on: {push: null}\njobs: {}\nflag: true\n", Loader=FORMATS.Loader)
        self.assertIn("on", value);self.assertIs(value["flag"], True)
        with self.assertRaises(ValueError):yaml.load("key: 1\nkey: 2\n", Loader=FORMATS.Loader)

    def test_workflow_dependency_cycles_and_missing_jobs_are_rejected(self):
        for jobs in [{"a":{"needs":["missing"],"runs-on":"ubuntu-24.04","steps":[]}},
                     {"a":{"needs":["b"]},"b":{"needs":["a"]}}]:
            with self.assertRaises(ValueError):FORMATS.workflow({"on":{"push":None},"jobs":jobs}, ROOT/'test.yml', ROOT, False)

    def test_workflow_invalid_step_and_reusable_job_shapes_fail(self):
        for job in [{"runs-on":"ubuntu-24.04","steps":[{"run":"true","uses":"a/b@v1"}]},
                    {"uses":"./missing.yml","steps":[]},
                    {"runs-on":"ubuntu-24.04","steps":[{"name":"missing command"}]}]:
            with self.assertRaises(ValueError):FORMATS.workflow({"on":{"push":None},"jobs":{"a":job}},ROOT/'test.yml',ROOT,False)

    def test_workflow_inline_shell_and_expression_syntax_are_checked(self):
        job={"runs-on":"ubuntu-24.04","steps":[{"run":"if then; echo bad"}]}
        with self.assertRaises(Exception):FORMATS.workflow({"on":{"push":None},"jobs":{"a":job}},ROOT/'test.yml',ROOT,False)
        for value in ["${{ }}", "${{ github.ref", "${{ contains(github.ref }}"]:
            with self.assertRaises(ValueError):FORMATS.expression(value)
        FORMATS.expression("${{ format('test-{0}', github.run_id) }}")

    def test_dockerfile_json_copy_context_and_stage_contracts(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'input').write_text('payload')
            FORMATS.dockerfile('ARG VERSION=1\nFROM scratch AS base\nCOPY input /input\nENTRYPOINT ["/input"]\n',root)
            for text in ['FROM scratch\nRUN ["bad",]\n', 'FROM scratch\nCOPY missing /input\n',
                         'FROM scratch AS base\nFROM scratch\nCOPY --from=unknown /x /x\n',
                         'FROM scratch\nINVALID test\n','RUN ["true"]\n','FROM scratch\\\n']:
                with self.subTest(text=text), self.assertRaises(ValueError):FORMATS.dockerfile(text,root)

    def test_markdown_local_files_are_checked_without_network(self):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/'README.md'
            FORMATS.markdown('[external](https://example.com) [anchor](#test)',path)
            with self.assertRaises(ValueError):FORMATS.markdown('![missing](assets/missing.png)',path)

    def test_png_integrity_checks_detect_truncation(self):
        image=(ROOT/'assets/banner.png').read_bytes()
        FORMATS.png(image)
        for damaged in [b'not png',image[:20],image[:-3],image[:50]+bytes([image[50]^1])+image[51:]]:
            with self.assertRaises(ValueError):FORMATS.png(damaged)

    def test_registered_configuration_types_reject_malformed_inputs(self):
        bad={"bad.toml":"[bad", "bad.svg":"<svg>", "bad.csv":"a,b\n1\n", "bad.env":"KEY=a\nKEY=b\n", "bad.jq":".[["}
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            for name,text in bad.items():
                path=root/name;path.write_text(text)
                with self.subTest(name=name), self.assertRaises(Exception):FORMATS.validate(path,root,False)

    def test_shell_regression_children_are_first_class_analyzed_files(self):
        text=(ROOT/'ci/tests/run.sh').read_text()
        for fixture in ['background-group.sh','failed-registration.sh']:
            self.assertIn('ci/tests/fixtures/'+fixture,text)
            self.assertEqual(kind(ROOT/'ci/tests/fixtures'/fixture),'shell')

    def test_every_shipped_file_has_a_registered_analyzer(self):
        inventory=json.loads((ROOT / "licenses/assets.json").read_text())
        paths=[entry["path"] for entry in inventory["assets"]]
        self.assertEqual(len(paths),len(set(paths)))
        for path in files():
            with self.subTest(path=str(path.relative_to(ROOT))):self.assertIsInstance(kind(path),str)


if __name__ == '__main__':
    unittest.main(verbosity=2)
