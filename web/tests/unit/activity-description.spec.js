import { expect } from 'chai';
import { activityDescription, parseAuditDescription } from '@/lib/enhanced/activity';

const t = (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key);

describe('activity feed descriptions', () => {
  it('renders an audit record as a sentence', () => {
    const json = JSON.stringify({
      event_id: 'e1', action: 'project_role_assign', target_type: 'project_user', target_id: '4', outcome: 'allowed',
    });
    expect(activityDescription(json, t)).to.equal(
      `activityAuditEvent:${JSON.stringify({ action: 'project role assign', target: 'project user 4', outcome: 'activityOutcome_allowed' })}`,
    );
  });

  it('passes plain descriptions and malformed JSON through', () => {
    expect(activityDescription('Sam Weber (sam) added to team', t)).to.equal('Sam Weber (sam) added to team');
    expect(activityDescription('{not json', t)).to.equal('{not json');
    expect(parseAuditDescription('{"a":1}')).to.equal(null);
  });
});
