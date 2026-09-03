import { expect } from 'chai';
import fs from 'fs';
import path from 'path';

import reachableUISources from './reachable-ui-sources';

describe('full-product UI contract', () => {
  it('contains no commercial subscription or edition gates', () => {
    const sourceRoot = path.resolve(process.cwd(), 'src');
    const source = reachableUISources(sourceRoot)
      .map((filename) => fs.readFileSync(filename, 'utf8'))
      .join('\n');

    expect(source).not.to.match(/Subscription(Form|Label)|i-subscription|upgradeToPro/);
    expect(source).not.to.match(/VUE_APP_(BUILD_TYPE|EDITION)|isEnhancedEdition|\bisPro\b/);
    expect(source).not.to.match(/semaphoreui\.com\/(portal|enterprise)/);
    expect(fs.existsSync(path.join(sourceRoot, 'components/SubscriptionForm.vue'))).to.equal(false);
    expect(fs.existsSync(path.join(sourceRoot, 'components/SubscriptionLabel.vue'))).to.equal(false);
  });
});
