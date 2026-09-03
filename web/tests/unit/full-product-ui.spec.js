import { expect } from 'chai';
import fs from 'fs';
import path from 'path';

const collectSources = (directory) => fs.readdirSync(directory, { withFileTypes: true })
  .flatMap((entry) => {
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      return entry.name === 'lang' ? [] : collectSources(filename);
    }
    return /\.(js|vue)$/.test(entry.name) ? [filename] : [];
  });

describe('full-product UI contract', () => {
  it('contains no commercial subscription or edition gates', () => {
    const sourceRoot = path.resolve(process.cwd(), 'src');
    const source = collectSources(sourceRoot)
      .map((filename) => fs.readFileSync(filename, 'utf8'))
      .join('\n');

    expect(source).not.to.match(/Subscription(Form|Label)|i-subscription|upgradeToPro/);
    expect(source).not.to.match(/VUE_APP_(BUILD_TYPE|EDITION)|isEnhancedEdition|\bisPro\b/);
    expect(source).not.to.match(/semaphoreui\.com\/(portal|enterprise)/);
    expect(fs.existsSync(path.join(sourceRoot, 'components/SubscriptionForm.vue'))).to.equal(false);
    expect(fs.existsSync(path.join(sourceRoot, 'components/SubscriptionLabel.vue'))).to.equal(false);
  });
});
