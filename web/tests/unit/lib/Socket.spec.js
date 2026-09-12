import { expect } from 'chai';
import Socket from '@/lib/Socket';
import FakeWebSocket from '@/lib/FakeWebSocket';

function createFakeCreator() {
  const created = [];
  const creator = () => {
    const ws = {
      closed: false,
      close() {
        this.closed = true;
      },
    };
    created.push(ws);
    return ws;
  };
  return { creator, created };
}

describe('Socket', () => {
  it('uses a fake WebSocket when started without an active session', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);

    socket.start();

    expect(socket.isRunning()).to.equal(true);
    expect(socket.ws).to.be.instanceOf(FakeWebSocket);
    expect(created).to.have.lengthOf(0);
  });

  it('creates a real WebSocket when the session is active', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);

    socket.setSessionActive(true);
    socket.start();

    expect(created).to.have.lengthOf(1);
    expect(socket.ws).to.equal(created[0]);
  });

  it('switches from fake to real WebSocket when the session becomes active', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);

    socket.start();
    expect(socket.ws).to.be.instanceOf(FakeWebSocket);

    socket.setSessionActive(true);

    expect(created).to.have.lengthOf(1);
    expect(socket.ws).to.equal(created[0]);
  });

  it('dispatches parsed messages to listeners', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);
    const received = [];
    socket.addListener((data) => received.push(data));

    socket.setSessionActive(true);
    socket.start();
    created[0].onmessage({ data: JSON.stringify({ type: 'update', id: 7 }) });

    expect(received).to.deep.equal([{ type: 'update', id: 7 }]);
  });

  it('closes the WebSocket on stop', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);

    socket.setSessionActive(true);
    socket.start();
    socket.stop();

    expect(created[0].closed).to.equal(true);
    expect(socket.isRunning()).to.equal(false);
  });

  it('closes the WebSocket when the session becomes inactive', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);

    socket.setSessionActive(true);
    socket.start();
    socket.setSessionActive(false);

    expect(created[0].closed).to.equal(true);
    expect(socket.isRunning()).to.equal(false);
  });
});

describe('Socket reconnect', () => {
  const originalSetTimeout = global.setTimeout;
  let scheduled;

  beforeEach(() => {
    scheduled = [];
    global.setTimeout = (cb, ms) => {
      scheduled.push({ cb, ms });
      return scheduled.length;
    };
  });

  afterEach(() => {
    global.setTimeout = originalSetTimeout;
  });

  it('schedules a reconnect when the connection closes while running', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);
    socket.setSessionActive(true);
    socket.start();

    created[0].onclose();

    expect(socket.isRunning()).to.equal(false);
    expect(scheduled).to.have.lengthOf(1);
    expect(scheduled[0].ms).to.equal(2000);

    scheduled[0].cb();

    expect(created).to.have.lengthOf(2);
    expect(socket.ws).to.equal(created[1]);
  });

  it('does not reconnect if the session became inactive meanwhile', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);
    socket.setSessionActive(true);
    socket.start();

    created[0].onclose();
    socket.sessionActive = false;
    scheduled[0].cb();

    expect(created).to.have.lengthOf(1);
    expect(socket.isRunning()).to.equal(false);
  });

  it('does not reconnect after stop()', () => {
    const { creator, created } = createFakeCreator();
    const socket = new Socket(creator);
    socket.setSessionActive(true);
    socket.start();
    const ws = created[0];

    socket.stop();
    ws.onclose();

    expect(scheduled).to.have.lengthOf(0);
    expect(created).to.have.lengthOf(1);
  });
});

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
