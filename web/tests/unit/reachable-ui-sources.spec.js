import { expect } from 'chai';
import fs from 'fs';
import os from 'os';
import path from 'path';
import reachableUISources from './reachable-ui-sources';

function fixture(files, verify) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'ui-source-graph-'));
  try {
    Object.entries(files).forEach(([name, contents]) => {
      const filename = path.join(directory, name);
      fs.mkdirSync(path.dirname(filename), { recursive: true });
      fs.writeFileSync(filename, contents);
    });
    verify(directory);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
}

describe('selected UI source graph', () => {
  it('follows directory Vue entries and lazy imports while excluding unwired files', () => {
    fixture({
      'main.js': "import '@/components/panel'; import('./lazy');",
      'components/panel/index.vue': '<template><div>Selected</div></template>',
      'lazy.js': "import './main';",
      'unwired.vue': '<template><div>Not selected</div></template>',
    }, (directory) => {
      const selected = reachableUISources(directory)
        .map((filename) => path.relative(directory, filename));
      expect(selected).to.have.members(['main.js', 'components/panel/index.vue', 'lazy.js']);
    });
  });

  it('fails closed when a local import cannot be resolved', () => {
    fixture({ 'main.js': "import './missing';" }, (directory) => {
      expect(() => reachableUISources(directory)).to.throw('Unresolved local UI import');
    });
  });

  it('requires review of computed imports and context loaders', () => {
    ["import('./views/' + page);", 'require(component);', "require.context('./views', true, /vue$/);"].forEach((source) => {
      fixture({ 'main.js': source }, (directory) => {
        expect(() => reachableUISources(directory)).to.throw('explicit inventory');
      });
    });
  });
});
