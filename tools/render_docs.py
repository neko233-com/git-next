from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit
import os
import re

from markdown_it import MarkdownIt

root = Path(__file__).resolve().parents[1]
site = root / 'docs/site'
sources = {
    root / 'README.md': 'index.html',
    root / 'docs/COMPATIBILITY.md': 'compatibility.html',
    root / 'docs/INSTALLATION.md': 'installation.html',
    root / 'docs/BUILDING.md': 'building.html',
    root / 'docs/PERFORMANCE.md': 'performance.html',
    root / 'docs/ACCEPTANCE.md': 'acceptance.html',
    root / 'docs/TEST_COVERAGE.md': 'test-coverage.html',
    root / 'docs/TAKEOVER.md': 'takeover.html',
    root / 'docs/LFS.md': 'lfs.html',
    root / 'docs/TERMINAL.md': 'terminal.html',
    root / 'docs/GAME_PROJECTS.md': 'game-projects.html',
    root / 'docs/READ_COMMANDS.md': 'read-commands.html',
    root / 'docs/RESTORATION.md': 'restoration.html',
    root / 'docs/LINE_ENDINGS.md': 'line-endings.html',
    root / 'docs/NATIVE_DISCOVERY.md': 'native-discovery.html',
    root / 'docs/CREDENTIALS.md': 'credentials.html',
    root / 'docs/MERGE.md': 'merge.html',
    root / 'docs/UPDATES.md': 'updates.html',
    root / 'docs/LANGUAGE.md': 'language.html',
    root / 'docs/NATIVE_CORE.md': 'native-core.html',
    root / 'docs/AGENT_WORKFLOW.md': 'agents.html',
    root / 'docs/DELIVERY_STATUS.md': 'delivery-status.html',
    root / 'docs/CORRESPONDING_SOURCES.md': 'corresponding-sources.html',
    root / 'docs/DOCUMENTATION_SITE.md': 'documentation-site.html',
    root / 'docs/GIT-GUIDE.md': 'git-guide.html',
    root / 'docs/PRODUCTION-READINESS.md': 'production-readiness.html',
    root / 'docs/CLONE-BENCHMARK.md': 'clone-benchmark.html',
    root / 'docs/MSI-PROOF.md': 'msi-proof.html',
    root / 'docs/WHY-GIT-NEXT.md': 'why-git-next.html',
    root / 'docs/DEFAULTS.md': 'defaults.html',
}
parser = MarkdownIt('commonmark').enable('table')
for source, name in sources.items():
    target = site / name
    template = target if target.exists() else site / 'index.html'
    prefix = template.read_text(encoding='utf-8').split('<main>')[0] + '<main>'
    if 'href="test-coverage.html"' not in prefix:
        prefix = prefix.replace('</nav>', '<a href="test-coverage.html">Test coverage</a></nav>')
    if name == 'test-coverage.html':
        prefix = re.sub(r'<title>.*?</title>', '<title>git-next · Test coverage</title>', prefix)
    if name == 'takeover.html':
        prefix = re.sub(r'<title>.*?</title>', '<title>git-next · Complete takeover</title>', prefix)
    if 'href="takeover.html"' not in prefix:
        prefix = prefix.replace('</nav>', '<a href="takeover.html">Complete takeover</a></nav>')
    if name == 'lfs.html':
        prefix = re.sub(r'<title>.*?</title>', '<title>git-next · Native LFS</title>', prefix)
    if 'href="lfs.html"' not in prefix:
        prefix = prefix.replace('</nav>', '<a href="lfs.html">Native LFS</a></nav>')
    if name == 'read-commands.html':
        prefix = re.sub(r'<title>.*?</title>', '<title>git-next · Read commands</title>', prefix)
    if 'href="read-commands.html"' not in prefix:
        prefix = prefix.replace('</nav>', '<a href="read-commands.html">Read commands</a></nav>')
    for page, title in [('restoration.html', 'Restore and reset'), ('line-endings.html', 'LF policy'), ('native-discovery.html', 'Native discovery'), ('credentials.html', 'Credential support'), ('merge.html', 'Native merge'), ('updates.html', 'MSI and updates'), ('language.html', 'Interface languages'), ('native-core.html', 'Native core integration'), ('agents.html', 'Engineering workflow'), ('delivery-status.html', 'Delivery progress'), ('corresponding-sources.html', 'Runtime sources and licenses'), ('terminal.html', 'Git-Terminal'), ('game-projects.html', 'Game projects'), ('documentation-site.html', 'Documentation site'), ('git-guide.html', 'Git guide'), ('why-git-next.html', 'Why git-next'), ('production-readiness.html', 'Production readiness'), ('clone-benchmark.html', 'Clone measurement'), ('msi-proof.html', 'Installer proof'),
        ('defaults.html', 'Runtime defaults')]:
        if name == page:
            prefix = re.sub(r'<title>.*?</title>', f'<title>git-next · {title}</title>', prefix)
        if f'href="{page}"' not in prefix:
            prefix = prefix.replace('</nav>', f'<a href="{page}">{title}</a></nav>')
    rendered = parser.render(source.read_text(encoding='utf-8'))

    def fix(match):
        href = match.group(1)
        url = urlsplit(href)
        if url.scheme or url.netloc or not url.path:
            return match.group(0)
        destination = (source.parent / unquote(url.path)).resolve()
        if destination in sources:
            relative = sources[destination]
        else:
            relative = os.path.relpath(destination, site).replace('\\', '/')
        if url.fragment:
            relative += '#' + url.fragment
        return f'href="{relative}"'

    rendered = re.sub(r'href="([^"]*)"', fix, rendered)
    target.write_text(prefix + rendered + '</main></html>\n', encoding='utf-8', newline='\n')

class Links(HTMLParser):
    def handle_starttag(self, tag, attrs):
        for key, value in attrs:
            if key not in ('href', 'src') or not value:
                continue
            url = urlsplit(value)
            if not url.scheme and not url.netloc and url.path:
                assert (self.path.parent / unquote(url.path)).exists(), (self.path.name, value)

for name in sources.values():
    check = Links()
    check.path = site / name
    check.feed(check.path.read_text(encoding='utf-8'))
print(f'Rendered {len(sources)} HTML manuals; all local links resolve.')
