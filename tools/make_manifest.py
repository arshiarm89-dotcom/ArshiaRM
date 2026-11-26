#!/usr/bin/env python3
"""Writes manifest.json: the list of museum files with a fingerprint (SHA-1) and size for each.
The app compares it with its own list and downloads only the files that changed.
Usage: python3 tools/make_manifest.py <museum folder>      Created By ArshiaRM"""
import hashlib, json, os, sys, time

SKIP_DIRS = {'.git', '.github', 'tools', 'updates', 'node_modules'}
SKIP_FILES = {'manifest.json', 'README.md', '.nojekyll', '.gitattributes', '.gitignore'}

def main():
    root = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else '.')
    files = {}
    for d, dirs, names in os.walk(root):
        dirs[:] = sorted(x for x in dirs if x not in SKIP_DIRS and not x.startswith('.'))
        for n in sorted(names):
            if n in SKIP_FILES or n.startswith('.'):
                continue
            full = os.path.join(d, n)
            rel = os.path.relpath(full, root).replace(os.sep, '/')
            h = hashlib.sha1()
            with open(full, 'rb') as f:
                for chunk in iter(lambda: f.read(1 << 20), b''):
                    h.update(chunk)
            files[rel] = [h.hexdigest(), os.path.getsize(full)]
    path = os.path.join(root, 'manifest.json')
    old = {}
    if os.path.exists(path):
        with open(path, encoding='utf-8') as f:
            old = json.load(f)
    if old.get('files') == files:
        print('no change, version', old.get('version'))
        return
    man = {
        'name': 'Digital Museum - Created By ArshiaRM',
        'version': max(int(time.time()), int(old.get('version', 0)) + 1),
        'mirrors': old.get('mirrors', []),
        'files': files,
    }
    with open(path, 'w', encoding='utf-8') as f:
        json.dump(man, f, ensure_ascii=False, indent=0, sort_keys=False)
        f.write('\n')
    print('manifest written, version', man['version'], 'files', len(files))

if __name__ == '__main__':
    main()
