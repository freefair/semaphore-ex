import { expect } from 'chai';
import Socket from '@/lib/Socket';

function realSocket() {
  return {
    closeCalls: 0,
    close() {
      this.closeCalls += 1;
    },
  };
}

describe('Socket', () => {
  it('does not create a real transport before a session is active', () => {
    const originalWebSocket = global.WebSocket;
    global.WebSocket = { OPEN: 1 };
    let created = 0;
    const socket = new Socket(() => {
      created += 1;
      return realSocket();
    });

    try {
      socket.start();

      expect(created).to.equal(0);
      expect(socket.isRunning()).to.equal(true);
    } finally {
      socket.stop();
      global.WebSocket = originalWebSocket;
    }
  });

  it('delivers parsed messages only to registered listeners', () => {
    const transport = realSocket();
    const socket = new Socket(() => transport);
    const delivered = [];
    const first = socket.addListener((message) => delivered.push(['first', message]));
    socket.addListener((message) => delivered.push(['second', message]));

    try {
      socket.setSessionActive(true);
      socket.start();
      transport.onmessage({ data: '{"type":"task","id":7}' });

      expect(delivered).to.deep.equal([
        ['first', { type: 'task', id: 7 }],
        ['second', { type: 'task', id: 7 }],
      ]);
      expect(socket.removeListener(first)).to.equal(true);
      expect(socket.removeListener(first)).to.equal(false);

      transport.onmessage({ data: '{"type":"task","id":8}' });

      expect(delivered).to.deep.equal([
        ['first', { type: 'task', id: 7 }],
        ['second', { type: 'task', id: 7 }],
        ['second', { type: 'task', id: 8 }],
      ]);
    } finally {
      socket.stop();
    }
  });

  it('starts and stops the real transport with the active session', () => {
    const transport = realSocket();
    let created = 0;
    const socket = new Socket(() => {
      created += 1;
      return transport;
    });

    socket.setSessionActive(true);
    socket.start();

    expect(created).to.equal(1);
    expect(socket.isRunning()).to.equal(true);

    socket.setSessionActive(false);

    expect(transport.closeCalls).to.equal(1);
    expect(socket.isRunning()).to.equal(false);
  });
});
