#!/usr/bin/env python3
"""Build complete release archives with embedded UI. Python standard library only."""
import argparse, hashlib, json, os, pathlib, re, shutil, subprocess, tarfile, tempfile, zipfile
ROOT = pathlib.Path(__file__).resolve().parent.parent
TARGETS = [('linux','amd64'), ('linux','arm64'), ('darwin','amd64'), ('darwin','arm64'), ('windows','amd64')]
def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--version', default=(ROOT/'internal/version/VERSION').read_text().strip())
    parser.add_argument('--target', action='append', help='GOOS/GOARCH; omit to build all five')
    parser.add_argument('--output', default=str(ROOT/'release'))
    args=parser.parse_args(); version=args.version.removeprefix('v')
    if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?',version): parser.error('invalid version')
    if version != (ROOT/'internal/version/VERSION').read_text().strip(): parser.error('version must match internal/version/VERSION')
    if not (ROOT/'web/dist/index.html').is_file(): parser.error('run npm ci && npm run build first')
    targets=[tuple(x.split('/')) for x in args.target] if args.target else TARGETS
    if any(t not in TARGETS for t in targets): parser.error('unsupported target')
    output=pathlib.Path(args.output).resolve();output.mkdir(parents=True,exist_ok=True);assets=[]
    for goos,arch in targets:
        platform='macos' if goos=='darwin' else goos
        name=f'bgmcat-{platform}-{arch}'
        with tempfile.TemporaryDirectory() as temp:
            folder=pathlib.Path(temp)/name;folder.mkdir()
            binary='bgmcat.exe' if goos=='windows' else 'bgmcat'
            env=dict(os.environ,GOOS=goos,GOARCH=arch,CGO_ENABLED='0')
            subprocess.run(['go','build','-trimpath','-ldflags',f'-s -w -X github.com/SuInk/bgmcat/internal/version.Override={version}','-o',str(folder/binary),'./cmd/bgmcat'],cwd=ROOT,env=env,check=True)
            for f in ['README.md','LICENSE','THIRD_PARTY_NOTICES.md']:shutil.copy2(ROOT/f,folder/f)
            shutil.copytree(ROOT/'docs',folder/'docs')
            (folder/'scripts').mkdir()
            shutil.copy2(ROOT/'scripts/install-aria2.sh',folder/'scripts/install-aria2.sh')
            (folder/'VERSION').write_text(version+'\n')
            (folder/'run.sh').write_text('#!/bin/sh\nset -eu\ncd "$(dirname "$0")"\nexec ./bgmcat "$@"\n');(folder/'run.sh').chmod(0o755)
            (folder/'run.bat').write_text('@echo off\r\n"%~dp0bgmcat.exe" %*\r\n')
            archive=output/(name+('.zip' if goos=='windows' else '.tar.gz'))
            if goos=='windows':
                with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as z:
                    for path in sorted(folder.rglob('*')):
                        if path.is_file():z.write(path,path.relative_to(folder.parent))
            else:
                with tarfile.open(archive,'w:gz') as z:z.add(folder,arcname=name)
            assets.append(dict(name=archive.name,os=platform,arch=arch,size=archive.stat().st_size,sha256=hashlib.sha256(archive.read_bytes()).hexdigest()))
    (output/'SHA256SUMS').write_text(''.join(f"{a['sha256']}  {a['name']}\n" for a in assets))
    (output/'latest.json').write_text(json.dumps(dict(schema=1,version=version,tag='v'+version,prerelease='-' in version,assets=assets),indent=2)+'\n')
    print(f'Packaged {len(assets)} platforms in {output}')
if __name__=='__main__':main()
