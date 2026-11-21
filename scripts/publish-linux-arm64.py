#!/usr/bin/env python3
"""Add native Linux ARM64 assets without replacing another platform or the APK."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

SHARED = {'praimate-cli-linux-arm64', 'praimate-code-linux-arm64', 'praimate-graphify-linux-arm64'}
PLATFORM = SHARED | {'praimate-linux-arm64.tar.gz', 'praimate-server-linux-arm64.tar.gz'}


def digest(stream):
    value = hashlib.sha256()
    for block in iter(lambda: stream.read(1024*1024), b''):
        value.update(block)
    return value.hexdigest()


def package_hashes(directory, edition):
    expected = SHARED | {'praimate-linux-arm64.tar.gz'}
    if edition == 'all':
        expected |= {'praimate-server-linux-arm64.tar.gz'}
    if {p.name for p in directory.iterdir()} != expected:
        raise ValueError('Missing or unexpected ARM64 packages')
    hashes = {}
    for name in sorted(expected):
        with (directory/name).open('rb') as stream:
            hashes[name] = digest(stream)
    def arm_elf(stream):
        header = stream.read(20)
        if len(header) != 20 or header[:6] != b'\x7fELF\x02\x01' or int.from_bytes(header[18:20], 'little') != 183 or int.from_bytes(header[16:18], 'little') not in (2,3):
            raise ValueError('Expected an executable Linux AArch64 ELF')
    for name in SHARED:
        with (directory/name).open('rb') as stream:
            arm_elf(stream)
    for flavor, name in [('desktop','praimate-linux-arm64.tar.gz'), ('server','praimate-server-linux-arm64.tar.gz')]:
        if name not in expected:
            continue
        with tarfile.open(directory/name) as archive:
            def entry(name):
                member = archive.getmember('linux-arm64/'+name)
                if not member.isfile():
                    raise ValueError('Bundle entry must be a regular file')
                return archive.extractfile(member)
            with entry('praimate-edition') as stream:
                if stream.read(32).strip() != flavor.encode():
                    raise ValueError('Incorrect bundle edition')
            for binary in ['praimate','praimate-gui','wpc','praimate-cli','praimate-code','praimate-graphify']:
                with entry(binary) as stream:
                    arm_elf(stream)
                standalone = binary+'-linux-arm64'
                if standalone in hashes:
                    with entry(binary) as stream:
                        if digest(stream) != hashes[standalone]:
                            raise ValueError('Bundled and shared binaries differ')
    return hashes


def validate_release(tag, release):
    if not isinstance(tag,str) or not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?', tag):
        raise ValueError('Expected a combined release tag, e.g. v1.3.7')
    if release['tag_name'] != tag or release.get('draft'):
        raise ValueError('Expected an existing published release')


def merge_checksums(release, manifest, hashes):
    assets = {a['name']: a for a in release['assets']}
    if assets['SHA256SUMS'].get('digest') != 'sha256:'+hashlib.sha256(manifest.encode()).hexdigest():
        raise ValueError('Existing manifest digest mismatch')
    parsed = {}
    for line in manifest.splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([^/\\\r\n]+)', line)
        if not match or match[2] in parsed:
            raise ValueError('Invalid existing checksum manifest')
        parsed[match[2]] = match[1]
    if not (set(assets)-PLATFORM-{'SHA256SUMS'}) <= parsed.keys():
        raise ValueError('Existing platform checksums are missing')
    for name, value in parsed.items():
        if name not in assets or assets[name].get('digest') != 'sha256:'+value:
            raise ValueError('Existing asset digest mismatch: '+name)
    for name, value in hashes.items():
        if name in parsed and parsed[name] != value:
            raise ValueError('Refusing to replace an already checksummed package: '+name)
    kept = [line for line in manifest.splitlines(keepends=True) if line.rstrip('\r\n').split('  ',1)[1] not in hashes]
    if kept and not kept[-1].endswith('\n'):
        kept[-1] += '\n'
    return ''.join(kept)+''.join(value+'  '+name+'\n' for name,value in sorted(hashes.items()))


def gh(*args, data=None):
    cmd = ['gh',*args]
    if data is not None:
        cmd += ['--input','-']
    result = subprocess.run(cmd,input=json.dumps(data) if data is not None else None,capture_output=True,text=True)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return result.stdout


def release_info(repo, tag):
    return json.loads(gh('api',f'repos/{repo}/releases/tags/{tag}'))


def protected(release):
    return {a['name']:(a['id'],a.get('digest'),a['size']) for a in release['assets'] if a['name'] not in PLATFORM | {'SHA256SUMS'}}


def snapshot(release):
    return {a['name']:(a['id'],a.get('digest'),a['size']) for a in release['assets']}


def tag_commit(repo, tag):
    obj = json.loads(gh('api',f'repos/{repo}/git/ref/tags/{tag}'))['object']
    for _ in range(4):
        if obj['type']=='commit':
            return obj['sha']
        if obj['type']!='tag':
            break
        obj = json.loads(gh('api',f'repos/{repo}/git/tags/{obj["sha"]}'))['object']
    raise ValueError('Release tag does not resolve to a commit')


def publish(repo, tag, directory, edition, source):
    if not re.fullmatch('[0-9a-f]{40}', source):
        raise ValueError('Expected the original source commit SHA')
    hashes = package_hashes(directory, edition)
    initial = release_info(repo, tag)
    validate_release(tag, initial)
    if tag_commit(repo,tag)!=source:
        raise ValueError('Release tag no longer matches the compiled source')
    with tempfile.TemporaryDirectory() as temp:
        temp = Path(temp)
        gh('release','download',tag,'--repo',repo,'--pattern','SHA256SUMS','--dir',str(temp))
        manifest = (temp/'SHA256SUMS').read_bytes().decode('utf-8')
        merged = merge_checksums(initial,manifest,hashes)
        current = release_info(repo,tag)
        validate_release(tag,current)
        if snapshot(current) != snapshot(initial):
            raise ValueError('Release changed during validation; retry with a fresh manifest')
        existing = {a['name']:a for a in initial['assets']}
        # Matching files are safe on a publisher retry. Uncommitted partial
        # uploads may be replaced; a checksummed package is never overwritten.
        for name,value in sorted(hashes.items()):
            if existing.get(name,{}).get('digest') != 'sha256:'+value:
                gh('release','upload',tag,str(directory/name),'--repo',repo,'--clobber')
        current = release_info(repo,tag)
        validate_release(tag,current)
        old_manifest = next(a for a in initial['assets'] if a['name']=='SHA256SUMS')
        new_manifest = next(a for a in current['assets'] if a['name']=='SHA256SUMS')
        if protected(current)!=protected(initial) or new_manifest['id']!=old_manifest['id'] or new_manifest.get('digest')!=old_manifest.get('digest'):
            raise ValueError('Another publisher changed the release; checksum update aborted')
        (temp/'SHA256SUMS').write_bytes(merged.encode('utf-8'))
        if merged != manifest:
            gh('release','upload',tag,str(temp/'SHA256SUMS'),'--repo',repo,'--clobber')
        final = release_info(repo,tag)
        validate_release(tag,final)
        merge_checksums(final,merged,hashes)
        if protected(final)!=protected(initial):
            raise ValueError('Unrelated release assets changed')
        marker = '<!-- praimate-linux-arm64 -->'
        body = final.get('body') or ''
        if marker not in body:
            body += ('\n\n'+marker+'\n### Linux ARM64\n\n'
                     'Native ARM64 Desktop and Server packages (where supported by the tagged source), '
                     'plus shared CLI, PrAImate Code and Graphify tools. Requires GTK 3 and WebKitGTK 4.1. '
                     'Built on Ubuntu 22.04 ARM64 from the original release source; other platform assets and the APK are unchanged.\n\n'
                     f'Source: [{source[:8]}](https://github.com/{repo}/commit/{source}).\n')
            gh('api',f'repos/{repo}/releases/{final["id"]}','--method','PATCH',data={'body':body})
    print('Published '+', '.join(sorted(hashes))+'; existing platform assets and APK retained.')


if __name__=='__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('tag',nargs='?')
    parser.add_argument('directory',nargs='?',type=Path)
    parser.add_argument('--source')
    parser.add_argument('--edition',choices=['all','desktop'],default='all')
    parser.add_argument('--check-release')
    parser.add_argument('--verify-packages',type=Path)
    args = parser.parse_args()
    if args.verify_packages:
        print(json.dumps(package_hashes(args.verify_packages,args.edition),sort_keys=True))
    else:
        repo = os.environ.get('GH_REPO','')
        if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+',repo):
            parser.error('Set GH_REPO to owner/repository')
        tag = args.check_release or args.tag
        # Validate before constructing an API path or any mutation.
        validate_release(tag,{'tag_name':tag,'draft':False})
        if args.check_release:
            validate_release(tag,release_info(repo,tag))
        elif args.directory and args.source:
            publish(repo,tag,args.directory,args.edition,args.source)
        else:
            parser.error('Expected TAG DIRECTORY --source SHA')
