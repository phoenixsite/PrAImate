import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('publisher',Path(__file__).with_name('publish-linux-arm64.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)


class LinuxARM64Publication(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name)

    def packages(self):
        directory=self.root/'packages';directory.mkdir()
        header=bytearray(64);header[:6]=b'\x7fELF\x02\x01';header[16:18]=(2).to_bytes(2,'little');header[18:20]=(183).to_bytes(2,'little')
        binaries={name:bytes(header)+name.encode() for name in ['praimate','praimate-gui','wpc','praimate-cli','praimate-code','praimate-graphify']}
        for name in m.SHARED:(directory/name).write_bytes(binaries[name.removesuffix('-linux-arm64')])
        for flavor,prefix in [('desktop','praimate'),('server','praimate-server')]:
            with tarfile.open(directory/(prefix+'-linux-arm64.tar.gz'),'w:gz') as archive:
                for name,body in {**binaries,'praimate-edition':flavor.encode()+b'\n'}.items():
                    entry=tarfile.TarInfo('linux-arm64/'+name);entry.size=len(body);entry.mode=0o755
                    archive.addfile(entry,io.BytesIO(body))
        return directory

    def release(self):
        contents={'praimate-mobile-preview.apk':b'apk','ANDROID-VERSION':b'<!-- praimate-android-version:1.3.7 -->\n','praimate-linux-amd64.tar.gz':b'desktop','praimate-server-linux-amd64.tar.gz':b'server'}
        manifest=''.join(hashlib.sha256(data).hexdigest()+'  '+name+'\n' for name,data in contents.items())
        contents['SHA256SUMS']=manifest.encode()
        assets=[{'id':i+1,'name':name,'size':len(data),'digest':'sha256:'+hashlib.sha256(data).hexdigest(),'download_count':0} for i,(name,data) in enumerate(contents.items())]
        return {'id':123,'tag_name':'v1.3.7','draft':False,'prerelease':False,'body':'Original notes\n<!-- praimate-android-version:1.3.7 -->','assets':assets},contents

    def test_native_packages_and_shared_copies_are_verified(self):
        directory=self.packages()
        self.assertEqual(set(m.package_hashes(directory,'all')),m.PLATFORM)
        wrong=bytearray((directory/'praimate-code-linux-arm64').read_bytes());wrong[18:20]=(62).to_bytes(2,'little')
        (directory/'praimate-code-linux-arm64').write_bytes(wrong)
        with self.assertRaisesRegex(ValueError,'AArch64'):m.package_hashes(directory,'all')

    def test_missing_or_mismatched_bundle_is_rejected(self):
        directory=self.packages()
        data=bytearray((directory/'praimate-cli-linux-arm64').read_bytes());data[-1]^=1
        (directory/'praimate-cli-linux-arm64').write_bytes(data)
        with self.assertRaisesRegex(ValueError,'differ'):m.package_hashes(directory,'all')
        (directory/'praimate-server-linux-arm64.tar.gz').unlink()
        with self.assertRaisesRegex(ValueError,'Missing'):m.package_hashes(directory,'all')

    def test_merge_preserves_every_old_checksum_and_refuses_changed_published_packages(self):
        release,contents=self.release();manifest=contents['SHA256SUMS'].decode()
        hashes={'praimate-cli-linux-arm64':'a'*64}
        merged=m.merge_checksums(release,manifest,hashes)
        self.assertTrue(merged.startswith(manifest))
        asset={'id':99,'name':'praimate-cli-linux-arm64','size':1,'digest':'sha256:'+'a'*64}
        release['assets'].append(asset)
        next(a for a in release['assets'] if a['name']=='SHA256SUMS')['digest']='sha256:'+hashlib.sha256(merged.encode()).hexdigest()
        self.assertEqual(m.merge_checksums(release,merged,hashes),merged)
        with self.assertRaisesRegex(ValueError,'Refusing'):m.merge_checksums(release,merged,{'praimate-cli-linux-arm64':'b'*64})
        with self.assertRaisesRegex(ValueError,'manifest digest'):m.merge_checksums(release,manifest,hashes)

    def test_drafts_and_invalid_tags_are_rejected(self):
        for tag in [None,'main','v1.3.7/../../other','server-v1.3.7','v1.3.7\n']:
            with self.assertRaises(ValueError):m.validate_release(tag,{'tag_name':tag,'draft':False})
        with self.assertRaises(ValueError):m.validate_release('v1.3.7',{'tag_name':'v1.3.7','draft':True})

    def exercise_publication(self,change_protected=False,wrong_source=False):
        directory=self.packages();release,contents=self.release();original=copy.deepcopy(release)
        uploads=[]
        def fake(*args,data=None):
            if args[0]=='api':
                if '/git/ref/tags/' in args[1]:return json.dumps({'object':{'type':'commit','sha':'a'*40}})
                if '--method' in args:
                    release['body']=data['body'];return '{}'
                for a in release['assets']:a['download_count']=a.get('download_count',0)+1
                return json.dumps(release)
            if args[1]=='download':
                (Path(args[args.index('--dir')+1])/'SHA256SUMS').write_bytes(contents['SHA256SUMS']);return ''
            if args[1]=='upload':
                file=Path(args[3]);name=file.name;uploads.append(name);contents[name]=file.read_bytes()
                release['assets']=[a for a in release['assets'] if a['name']!=name]+[{'id':100+len(uploads),'name':name,'size':len(contents[name]),'digest':'sha256:'+hashlib.sha256(contents[name]).hexdigest()}]
                if change_protected:
                    next(a for a in release['assets'] if a['name']=='praimate-mobile-preview.apk')['size']+=1
                return ''
            raise AssertionError(args)
        with patch.object(m,'gh',fake):
            if change_protected or wrong_source:
                with self.assertRaises(ValueError):m.publish('owner/repo','v1.3.7',directory,'all',('b' if wrong_source else 'a')*40)
                self.assertNotIn('SHA256SUMS',uploads)
                if wrong_source:self.assertEqual(uploads,[])
            else:
                m.publish('owner/repo','v1.3.7',directory,'all','a'*40)
                self.assertEqual(set(uploads),m.PLATFORM|{'SHA256SUMS'})
                self.assertEqual(m.protected(original),m.protected(release))
                self.assertEqual(contents['praimate-mobile-preview.apk'],b'apk')
                self.assertEqual(release['body'].count('<!-- praimate-android-version:1.3.7 -->'),1)
                self.assertTrue(release['body'].startswith(original['body']))
                uploads.clear();m.publish('owner/repo','v1.3.7',directory,'all','a'*40)
                self.assertEqual(uploads,[])
                self.assertEqual(release['body'].count('<!-- praimate-linux-arm64 -->'),1)

    def test_publication_is_additive_and_retry_is_idempotent(self):self.exercise_publication()
    def test_concurrent_unrelated_update_aborts_manifest_replacement(self):self.exercise_publication(change_protected=True)
    def test_wrong_tag_source_aborts_before_upload(self):self.exercise_publication(wrong_source=True)


if __name__=='__main__':unittest.main()
