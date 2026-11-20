#!/usr/bin/env python3
"""Run explicit static checks for every first-party language and embedded filter."""

import argparse
import importlib.util
import re
import shlex
import subprocess
import sys

import yaml
from source_files import ROOT, files, kind

SPEC = importlib.util.spec_from_file_location('formats', ROOT/'ci/analyze-formats.py')
FORMATS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FORMATS)


def run(command, **kwargs):
    subprocess.run(command, check=True, cwd=ROOT, timeout=180, **kwargs)


def shell(language):
    found = []
    for path in files():
        if kind(path) != 'shell':
            continue
        first = path.read_text().splitlines()[0]
        dialect = 'sh' if first in {'#!/bin/sh', '#!/usr/bin/env sh'} else 'bash'
        if (language == 'posix') != (dialect == 'sh'):
            continue
        run([dialect, '-n', str(path)])
        run(['shellcheck', '--external-sources', '--severity=style', '--shell='+dialect, str(path)])
        found.append(path)
    print('PASS:', language, len(found), 'scripts')


def programs(text, language):
    """Locate literal programs after jq/awk/sed options in shell source."""
    lex = shlex.shlex(text, posix=True, punctuation_chars='|;&()<>')
    lex.whitespace_split = True
    tokens = list(lex)
    for index, token in enumerate(tokens):
        if token != language:
            continue
        j = index + 1
        while j < len(tokens):
            value = tokens[j]
            if not value.strip():
                j += 1
                continue
            if value in {'-f', '--file', '--from-file'}:
                break  # Standalone filter files are discovered independently.
            if value in {'--arg', '--argjson', '--rawfile', '--slurpfile'}:
                j += 3
            elif value in {'-v', '-F'}:
                j += 2
            elif value in {'-e', '--expression'} and language == 'sed':
                j += 1
                if j < len(tokens):
                    yield tokens[j]
                j += 1
            elif value.startswith('-'):
                j += 1
            elif value in {'|', ';', '&&', '||', ')', '>'}:
                break
            else:
                if '\n' in value or (language == 'jq' and (any(c in value for c in '.[$({') or value in {'length','type','empty','keys','values','true','false','null'})) or (language == 'awk' and '{' in value) or (language == 'sed' and value.startswith(('s', '/', 'y'))):
                    yield value
                break


def check_program(program, language):
    if language == 'jq':
        variables = sorted(set(re.findall(r'\$([A-Za-z_][A-Za-z0-9_]*)', program)))
        args = [part for name in variables for part in ['--argjson', name, 'null']]
        run(['jq', '-n', *args, 'try ('+program+') catch null'], stdout=subprocess.DEVNULL)
    elif language == 'awk':
        # Programs in this project only transform input and print summaries.
        # Empty input exercises the real parser and never contacts an external service.
        result = subprocess.run(['awk', program, '/dev/null'], cwd=ROOT, capture_output=True, text=True, timeout=5)
        if result.returncode not in {0, 1} or result.stderr:
            raise ValueError('awk syntax check: '+result.stderr)
    else:
        run(['sed', '-n', '-e', program, '/dev/null'], stdout=subprocess.DEVNULL)


def filters(language):
    count = 0
    for path in files():
        category = kind(path)
        if category == language:
            check_program(path.read_text(), language)
            count += 1
        elif category == 'shell':
            for program in programs(path.read_text(), language):
                check_program(program, language)
                count += 1
    print('PASS:', language, count, 'standalone/embedded programs (zero means not used)')


def modules():
    # Vendor mode must resolve all packages offline and match go.mod/modules.txt.
    for args in [['go','mod','edit','-json'], ['go','list','-buildvcs=false','-mod=vendor','-deps','./...']]:
        subprocess.run(args, cwd=ROOT/'build', check=True, timeout=180, stdout=subprocess.DEVNULL,
                       env=__import__('os').environ | {'CGO_ENABLED':'0','GOTOOLCHAIN':'local','GOWORK':'off'})
    print('PASS: module syntax, offline package graph and vendor consistency')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('language', choices=['go','python','bash','posix','jq','awk','sed','dockerfile','yaml','actions','csv','json','modules','formats'])
    language = parser.parse_args().language
    if language == 'go':
        run(['bash','ci/analyze-go.sh'])
    elif language == 'python':
        run(['python3','-B','ci/analyze-python.py'])
        run(['python3','-B','ci/check_licenses.py'])
    elif language in {'bash','posix'}:
        shell(language)
    elif language in {'jq','awk','sed'}:
        filters(language)
    elif language == 'modules':
        modules()
    elif language == 'formats':
        run(['python3','-B','ci/analyze-formats.py'])
        run(['python3','-B','ci/test_static.py'])
    else:
        count = 0
        for path in files():
            category = kind(path)
            if language == 'actions':
                if category == 'yaml' and path.parent == ROOT/'.github/workflows':
                    FORMATS.workflow(yaml.load(path.read_text(), Loader=FORMATS.Loader), path, ROOT)
                    count += 1
            elif category == language:
                FORMATS.validate(path)
                count += 1
        print('PASS:', language, count, 'files')
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.SubprocessError, yaml.YAMLError) as error:
        print('Static analysis failed:', error, file=sys.stderr)
        sys.exit(1)
