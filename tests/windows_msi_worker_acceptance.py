"""Exercise the owned no-console MSI worker with generated scripts only."""
import json
from pathlib import Path
import shutil
import subprocess


def check_worker(binary, root, env):
    updater=Path(root)/'worker 中文 fixture/updater';updater.mkdir(parents=True)
    helper=updater/'msi-worker.exe';shutil.copyfile(binary,helper)
    (updater/'update.ps1').write_text("""param([string]$Mode,[string]$Request)
$path=Join-Path $PSScriptRoot 'result.json'
$count=1
if(Test-Path -LiteralPath $path){$count=(Get-Content -LiteralPath $path -Raw | ConvertFrom-Json).count+1}
$data=@{mode=$Mode;request=$Request;count=$count;input_redirected=[Console]::IsInputRedirected;output_redirected=[Console]::IsOutputRedirected}
Add-Type -TypeDefinition 'using System;using System.Runtime.InteropServices;public static class GnWorkerConsoleProbe{[DllImport("kernel32.dll")]public static extern IntPtr GetConsoleWindow();}'
$data.console_window=[GnWorkerConsoleProbe]::GetConsoleWindow().ToInt64()
[IO.File]::WriteAllText($path,($data | ConvertTo-Json),[Text.UTF8Encoding]::new($false))
exit 17
""",encoding='utf-8',newline='\n')
    run=lambda args:subprocess.run([str(helper),*args],env=env,capture_output=True,timeout=20).returncode
    assert run(['-Mode','Register'])==17,'Worker did not propagate the real script exit status'
    record=json.loads((updater/'result.json').read_text())
    assert record['mode']=='Register' and record['count']==1 and record['console_window']==0,'Worker did not preserve no-console execution'
    for request in ('Auto','Check','Apply','Enable','Disable',''):
        assert run(['-Mode','Scheduled','-Request',request])==17,'A supported scheduled request was rejected'
        result=json.loads((updater/'result.json').read_text())
        assert result['mode']=='Scheduled' and result['request']==(request or 'Auto') and result['console_window']==0
    for arguments in ([],['-Mode','Check'],['-Mode','Register','extra'],['-Mode','Register;&exit 0'],['-Mode','Scheduled','-Request','Apply;exit 0'],['-Mode','Register','-Request','Apply']):
        assert run(arguments)==87,'Worker accepted an unsupported command line'
    assert json.loads((updater/'result.json').read_text())['count']==7,'A failed script was retried or invalid arguments executed'
