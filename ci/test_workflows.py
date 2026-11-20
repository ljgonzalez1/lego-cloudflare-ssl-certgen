#!/usr/bin/env python3
"""Exercise the five CI event diagrams, advisory failures and hard publication gates."""

from pathlib import Path
import csv
import fnmatch
import re
import unittest

import yaml

ROOT = Path(__file__).resolve().parent.parent
STATIC_JOBS = {'static_'+name for name in ['go','python','bash','posix','jq','awk','sed','dockerfile','yaml','actions','csv','json','modules','formats']}
DOCUMENT_JOBS = {'version_readme','version_dockerhub'}
OFFLINE_JOBS = {'offline','go_tests'}
SMOKE_JOBS = {'smoke_armv6':'linux/arm/v6','smoke_armv7':'linux/arm/v7','smoke_arm64':'linux/arm64','smoke_i386':'linux/386','smoke_amd64':'linux/amd64'}


class Context(dict):
    def __getattr__(self, name):
        return self.get(name)


def context(value):
    if isinstance(value, dict):
        return Context({k:context(v) for k,v in value.items()})
    if isinstance(value, list):
        return [context(v) for v in value]
    return value


def accepts(expression, github, needs, *, failed=False, cancelled=False):
    expression=expression.removeprefix('${{').removesuffix('}}').strip()
    expression=expression.replace('&&',' and ').replace('||',' or ')
    expression=re.sub(r'!(?!=)', ' not ', expression)
    expression=re.sub(r'\btrue\b','True',expression)
    expression=re.sub(r'\bfalse\b','False',expression)
    return bool(eval('('+expression+')', {'__builtins__':{}}, {'github':context(github),'needs':context(needs),
                'always':lambda:True,'cancelled':lambda:cancelled,'failure':lambda:failed}))


class WorkflowChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow=yaml.safe_load((ROOT/'.github/workflows/tests.yml').read_text())
        cls.jobs=cls.workflow['jobs']

    def route(self,event,branch='master',*,base=None,created=False,deleted=False,failure=None,prerelease=False):
        github={'event_name':event,'ref':'refs/heads/'+branch,'base_ref':base or branch,
                'event':{'created':created,'deleted':deleted,'release':{'prerelease':prerelease}}}
        result={}
        for name,job in self.jobs.items():
            deps=job.get('needs',[])
            if isinstance(deps,str):deps=[deps]
            ready=all(result[dep]['result']=='success' for dep in deps)
            condition=job.get('if','true')
            allowed=(ready or 'cancelled()' in condition) and accepts(condition,github,result)
            result[name]={'result':('failure' if name==failure else 'success') if allowed else 'skipped'}
        return result

    def passed(self,*args,**kwargs):
        return {k for k,v in self.route(*args,**kwargs).items() if v['result']=='success'}

    def test_all_languages_have_explicit_static_jobs(self):
        self.assertEqual({x for x in self.jobs if x.startswith('static_')},STATIC_JOBS)
        for name in STATIC_JOBS:
            self.assertNotIn('needs',self.jobs[name])
            self.assertIn('ci/analyze.py '+name[7:],str(self.jobs[name]['steps']))

    def test_static_always_precedes_parallel_markdown_checks(self):
        names=list(self.jobs)
        for name in DOCUMENT_JOBS:
            self.assertEqual(set(self.jobs[name]['needs']),STATIC_JOBS)
            self.assertGreater(names.index(name),max(names.index(s) for s in STATIC_JOBS))
            self.assertNotIn('continue-on-error',str(self.jobs[name]['steps']))

    def test_first_push_to_new_branch_runs_but_branch_deletion_does_not(self):
        for branch in ['master','v6.0.1-beta-1','V6.0.1-beta-1','5.2.4', 'v1.2+rc.1']:
            expected=self.passed('push',branch)
            self.assertTrue(STATIC_JOBS|DOCUMENT_JOBS|OFFLINE_JOBS <= expected)
            self.assertEqual(self.passed('push',branch,created=True),expected)
            self.assertEqual(self.passed('push',branch,deleted=True),set())

    def test_version_push_merge_and_pr_check_documents_and_offline(self):
        for event in ['push','pull_request']:
            for branch in ['v6.0.1-beta-1', 'V6.0.1-beta-1', '5.2.4',
                           'v1.2-alpha', 'V1.2.3.4-BETA.10', '1.2-build-1',
                           'v1.2+rc.1', 'v1.2.3.RC-25']:
                self.assertEqual(self.passed(event,branch),STATIC_JOBS|DOCUMENT_JOBS|OFFLINE_JOBS)

    def test_master_push_and_pr_have_docs_offline_and_five_smokes(self):
        expected=STATIC_JOBS|DOCUMENT_JOBS|OFFLINE_JOBS|set(SMOKE_JOBS)
        for event in ['push','pull_request']:
            self.assertEqual(self.passed(event),expected)

    def test_published_release_and_prerelease_run_complete_graph(self):
        for prerelease in [False,True]:
            self.assertEqual(self.passed('release','v6.0.1-beta-1',prerelease=prerelease),set(self.jobs))
        triggers=self.workflow.get('on',self.workflow.get(True))
        self.assertEqual(triggers['release']['types'],['published'])

    def test_static_failure_is_blocking_only_for_pr_to_master(self):
        for failed in STATIC_JOBS:
            for event,branch in [('push','master'),('pull_request','master'),('push','v6.0.1-beta-1'),('pull_request','v6.0.1-beta-1'),('release','v6.0.1-beta-1')]:
                with self.subTest(failed=failed,event=event,branch=branch):
                    advisory=event!='pull_request' or branch!='master'
                    self.assertEqual(accepts(self.jobs[failed]['continue-on-error'],{'event_name':event,'base_ref':branch},{}),advisory)
                    passed=self.passed(event,branch,failure=failed)
                    self.assertEqual('offline' in passed,advisory)
                    self.assertEqual('publish' in passed,advisory and event=='release')

    def test_advisory_failures_remain_visible(self):
        for name in STATIC_JOBS:
            report=self.jobs[name]['steps'][-1]
            self.assertIn('::warning::',report['run'])
            self.assertIn('GITHUB_STEP_SUMMARY',report['run'])
            self.assertTrue(accepts(report['if'],{'event_name':'release','base_ref':''},{},failed=True))
            self.assertFalse(accepts(report['if'],{'event_name':'pull_request','base_ref':'master'},{},failed=True))

    def test_skipped_and_cancelled_static_jobs_cannot_publish(self):
        needs={name:{'result':'success'} for name in STATIC_JOBS|DOCUMENT_JOBS}
        for name in STATIC_JOBS:
            for status in ['skipped','cancelled']:
                mutated=needs|{name:{'result':status}}
                self.assertFalse(accepts(self.jobs['offline']['if'],{'event_name':'release','base_ref':''},mutated))
        self.assertFalse(accepts(self.jobs['offline']['if'],{'event_name':'release'},needs,cancelled=True))

    def test_document_and_functional_failures_block_publication(self):
        for failure in DOCUMENT_JOBS|OFFLINE_JOBS|set(SMOKE_JOBS):
            passed=self.passed('release',failure=failure)
            self.assertNotIn('publish',passed)
            self.assertNotIn('verify_hashes',passed)
            self.assertNotIn('verify_description',passed)

    def test_smokes_are_parallel_and_follow_offline_suites(self):
        for name,platform in SMOKE_JOBS.items():
            self.assertEqual(set(self.jobs[name]['needs']),OFFLINE_JOBS)
            self.assertEqual(self.jobs[name]['with']['platform'],platform)
        self.assertEqual(set(self.jobs['publish']['needs']),set(SMOKE_JOBS))

    def test_no_tests_subscribe_to_unrelated_branches_or_create(self):
        triggers=self.workflow.get('on',self.workflow.get(True))
        self.assertEqual(set(triggers),{'push','pull_request','release'})
        for trigger in ['push','pull_request']:
            self.assertIn('master',triggers[trigger]['branches'])
            self.assertNotIn('**',triggers[trigger]['branches'])
            # GitHub uses glob rather than regex branch filters. They must match
            # prereleases as well as plain versions; strict validation is in Bash.
            self.assertEqual(set(triggers[trigger]['branches']), {
                'master', '[vV][0-9]*.[0-9]*', '[0-9]*.[0-9]*',
            })

    def test_version_branch_globs_include_beta_alpha_rc_build_and_plus(self):
        triggers=self.workflow.get('on',self.workflow.get(True))
        valid=[
            'v1.2', 'V1.2.3', '1.2.3.4', 'v1.2-alpha', 'v1.2beta.1',
            'v1.2.3+build-3', 'V1.2.3.RC-25', 'v1.2+rc.1',
            'v1.2-audit', 'V1.2.3.4-ALFA.9',
        ]
        for event in ('push','pull_request'):
            globs=triggers[event]['branches']
            for branch in valid:
                with self.subTest(event=event,branch=branch):
                    self.assertTrue(any(fnmatch.fnmatchcase(branch,glob) for glob in globs))
            for branch in ('feature/my-feature','docs','other/v1.2.3','1', 'alpha'):
                with self.subTest(event=event,invalid=branch):
                    self.assertFalse(any(fnmatch.fnmatchcase(branch,glob) for glob in globs))

    def test_markdown_checks_are_never_skipped_for_version_branch_events(self):
        """Regression: beta/alpha pushes once skipped both markdown jobs silently."""
        branches = [
            'v6.0.1-beta-3', 'v6.0.1-beta-1', 'V6.0.1-beta-3',
            'v6.0.0-alpha-2', 'v1.2-alpha', '1.2-beta1', 'V1.2.3.RC-25',
        ]
        for event in ('push', 'pull_request'):
            for branch in branches:
                for created in ((False, True) if event == 'push' else (False,)):
                    with self.subTest(event=event, branch=branch, created=created):
                        reached = self.passed(event, branch, created=created)
                        self.assertTrue(DOCUMENT_JOBS.issubset(reached),
                                        f'Markdown checks skipped: {event} {branch} created={created}')
                        self.assertTrue(OFFLINE_JOBS.issubset(reached))

    def test_document_checks_apply_only_to_subscribed_events(self):
        triggers=self.workflow.get('on',self.workflow.get(True))
        self.assertEqual(set(triggers),{'push','pull_request','release'})
        for event,branch in [('push','master'),('push','v1.2-beta1'),
                             ('pull_request','master'),('pull_request','v1.2-alpha'),
                             ('release','v6.0.1-beta-1')]:
            with self.subTest(event=event,branch=branch):
                result=self.passed(event,branch)
                self.assertTrue(DOCUMENT_JOBS.issubset(result))
                self.assertTrue(OFFLINE_JOBS.issubset(result))
                if event != 'release':
                    self.assertNotIn('publish',result)
        for job in DOCUMENT_JOBS:
            script=self.jobs[job]['steps'][-1]['run']
            self.assertIn('ci/check-version.sh',script)
        for job in OFFLINE_JOBS:
            expression=self.jobs[job]['if']
            self.assertIn("needs.version_readme.result == 'success'",expression)
            self.assertIn("needs.version_dockerhub.result == 'success'",expression)
            self.assertNotIn("needs.version_readme.result == 'skipped'",expression)

    def test_both_propagation_waits_and_post_publication_paths_are_independent(self):
        for name in ['wait_image','wait_description']:
            script=self.jobs[name]['steps'][0]['run']
            self.assertIn('sleep 20',script)
        self.assertIn('verify_description',self.passed('release',failure='verify_image'))
        self.assertIn('verify_image',self.passed('release',failure='update_description'))

    def test_every_platform_digest_is_compared_to_buildx_output(self):
        job=self.jobs['verify_hashes']
        self.assertEqual(set(job['needs']),{'publish','verify_image'})
        entries=job['strategy']['matrix']['include']
        self.assertEqual({x['platform'] for x in entries},set(SMOKE_JOBS.values()))
        self.assertEqual(len(entries),5)
        self.assertIn('needs.publish.outputs',str(job))

    def test_nonblocking_static_is_not_a_functional_test_skip(self):
        for name in OFFLINE_JOBS|set(SMOKE_JOBS)|{'publish','verify_image','verify_hashes','verify_description'}:
            self.assertNotIn('continue-on-error',self.jobs[name])

    def test_default_image_version_matches_snapshot(self):
        with (ROOT/'ci/versions/versions.csv').open(newline='') as file:
            row=list(csv.DictReader(file))[-1]
        expected=row['App Version'].lstrip('vV')+('-'+row['App Suffix'] if row['App Suffix'] else '')
        config=(ROOT/'build/Dockerfile').read_text()
        if expected:
            self.assertIn('ARG APP_VERSION='+expected+'\n',config)


if __name__=='__main__':
    unittest.main()
