import { expect } from 'chai';
import {
  activityDescription, activityUsers, filterActivityByUser, parseAuditDescription,
} from '@/lib/enhanced/activity';

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

  it('lists each person once and filters the feed by one of them', () => {
    const items = [
      { username: 'kim', description: 'a' },
      { username: null, description: 'b' },
      { username: 'alice', description: 'c' },
      { username: 'kim', description: 'd' },
    ];
    expect(activityUsers(items)).to.deep.equal(['alice', 'kim']);
    expect(filterActivityByUser(items, 'kim').map((item) => item.description)).to.deep.equal(['a', 'd']);
    expect(filterActivityByUser(items, null)).to.have.length(4);
  });
});
