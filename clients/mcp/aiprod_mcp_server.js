#!/usr/bin/env node
/**
 * aiprod MCP Server — Exposes aiprod productivity tools via the Model Context Protocol.
 * Runs as a stdio MCP server that agents can use for memory, docs, knowledge, tasks, etc.
 */

const http = require('http');
const readline = require('readline');

const AIPROD_URL = process.env.AIPROD_URL || 'http://agentkit-aiprod:8600';
const AIPROD_KEY = process.env.AIPROD_API_KEY || '';
const AGENT_ID = process.env.AGENT_ID || '';
const SUB_AGENT_IDS = process.env.SUB_AGENT_IDS || '';
const MAILR_ADDRESS = process.env.AIPROD_MAILR_ADDRESS || '';

// --- HTTP helpers ---
function apiRequest(method, path, body, timeoutMs = 30000) {
    return new Promise((resolve, reject) => {
        const url = new URL(`${AIPROD_URL}/api/v1${path}`);
        const options = {
            hostname: url.hostname,
            port: url.port,
            path: url.pathname + url.search,
            method,
            headers: {
                'Content-Type': 'application/json',
                ...(AIPROD_KEY ? { 'Authorization': `Bearer ${AIPROD_KEY}` } : {}),
                ...(AGENT_ID ? { 'X-Agent-ID': AGENT_ID } : {}),
                ...(SUB_AGENT_IDS ? { 'X-Sub-Agent-IDs': SUB_AGENT_IDS } : {}),
            },
            timeout: timeoutMs,
        };
        const req = http.request(options, (res) => {
            let data = '';
            res.on('data', c => data += c);
            res.on('end', () => {
                try {
                    const json = JSON.parse(data);
                    if (json.ok === false && json.error) {
                        reject(new Error(json.error.message || json.error.code || 'API error'));
                    } else {
                        resolve(json.data);
                    }
                } catch(e) { resolve(data); }
            });
        });
        req.on('error', reject);
        req.on('timeout', () => { req.destroy(); reject(new Error('timeout')); });
        if (body) req.write(JSON.stringify(body));
        req.end();
    });
}
const get = (path, timeoutMs) => apiRequest('GET', path, null, timeoutMs);
const post = (path, body, timeoutMs) => apiRequest('POST', path, body, timeoutMs);
const patch = (path, body, timeoutMs) => apiRequest('PATCH', path, body, timeoutMs);

// --- Tool definitions ---
const TOOLS = [
    {
        name: 'memory_store',
        description: 'Store information in persistent memory for later recall.',
        inputSchema: {
            type: 'object',
            properties: {
                namespace: { type: 'string', description: 'Memory category (e.g. user-preferences, research-findings)' },
                key: { type: 'string', description: 'Short identifier for this memory' },
                value: { type: 'string', description: 'Content to remember' },
            },
            required: ['namespace', 'key', 'value'],
        },
    },
    {
        name: 'memory_recall',
        description: 'Search and recall stored memories.',
        inputSchema: {
            type: 'object',
            properties: {
                namespace: { type: 'string', description: 'Memory category to search (optional)' },
                query: { type: 'string', description: 'Search query (optional)' },
            },
        },
    },
    {
        name: 'create_document',
        description: 'Create a persistent, versioned document (report, summary, notes).',
        inputSchema: {
            type: 'object',
            properties: {
                title: { type: 'string', description: 'Document title' },
                content: { type: 'string', description: 'Document content (markdown)' },
                tags: { type: 'array', items: { type: 'string' }, description: 'Tags' },
            },
            required: ['title', 'content'],
        },
    },
    {
        name: 'search_documents',
        description: 'Search across stored documents.',
        inputSchema: {
            type: 'object',
            properties: {
                query: { type: 'string', description: 'Search query' },
            },
            required: ['query'],
        },
    },
    {
        name: 'add_knowledge',
        description: 'Store a verified fact as a knowledge triple (subject-predicate-object).',
        inputSchema: {
            type: 'object',
            properties: {
                subject: { type: 'string', description: 'The entity' },
                predicate: { type: 'string', description: 'The relationship' },
                object: { type: 'string', description: 'The value' },
                confidence: { type: 'number', description: 'Confidence 0.0-1.0' },
            },
            required: ['subject', 'predicate', 'object'],
        },
    },
    {
        name: 'query_knowledge',
        description: 'Query the knowledge graph for facts.',
        inputSchema: {
            type: 'object',
            properties: {
                subject: { type: 'string', description: 'Entity to look up' },
                predicate: { type: 'string', description: 'Relationship filter' },
            },
        },
    },
    {
        name: 'create_task',
        description: 'Create a task to track work. Returns the task with its generated ID (e.g. task_a1b2c3...). Save this ID to use with transition_task and update_task.',
        inputSchema: {
            type: 'object',
            properties: {
                title: { type: 'string', description: 'Task title' },
                description: { type: 'string', description: 'Task details' },
                assignee: { type: 'string', description: 'Agent or person to assign the task to' },
                priority: { type: 'string', description: 'Priority: low, medium, high, critical' },
                due_date: { type: 'string', description: 'Due date (RFC3339 or YYYY-MM-DD)' },
                parent_id: { type: 'string', description: 'Parent task ID for subtasks' },
                tags: { type: 'array', items: { type: 'string' }, description: 'Tags for categorization' },
            },
            required: ['title'],
        },
    },
    {
        name: 'update_task',
        description: 'Update a task\'s fields. The task_id must be an exact ID returned by create_task (e.g. task_a1b2c3...).',
        inputSchema: {
            type: 'object',
            properties: {
                task_id: { type: 'string', description: 'Exact task ID from create_task (e.g. task_a1b2c3d4e5f6...)' },
                title: { type: 'string', description: 'New title' },
                description: { type: 'string', description: 'New description' },
                assignee: { type: 'string', description: 'New assignee' },
                priority: { type: 'string', description: 'New priority: low, medium, high, critical' },
                due_date: { type: 'string', description: 'New due date' },
                tags: { type: 'array', items: { type: 'string' }, description: 'New tags' },
            },
            required: ['task_id'],
        },
    },
    {
        name: 'transition_task',
        description: 'Change a task\'s status. The task_id must be the exact ID returned by create_task (e.g. task_a1b2c3...), NOT a made-up ID. Valid transitions: open→in_progress/cancelled, in_progress→review/blocked/done/cancelled, blocked→in_progress/cancelled, review→in_progress/done/cancelled, done→open, cancelled→open.',
        inputSchema: {
            type: 'object',
            properties: {
                task_id: { type: 'string', description: 'Exact task ID from create_task (e.g. task_a1b2c3d4e5f6...). Must be a real ID, not a placeholder.' },
                status: { type: 'string', description: 'New status: open, in_progress, blocked, review, done, cancelled' },
            },
            required: ['task_id', 'status'],
        },
    },
    {
        name: 'list_tasks',
        description: 'List tasks, optionally filtered by status.',
        inputSchema: {
            type: 'object',
            properties: {
                status: { type: 'string', description: 'Filter by status (open, in_progress, done)' },
            },
        },
    },
    {
        name: 'send_email',
        description: 'Send an email from your assigned address.',
        inputSchema: {
            type: 'object',
            properties: {
                to: { type: 'array', items: { type: 'string' }, description: 'Recipient email addresses' },
                subject: { type: 'string', description: 'Email subject' },
                body: { type: 'string', description: 'Plain text body' },
                cc: { type: 'array', items: { type: 'string' }, description: 'CC addresses (optional)' },
                html: { type: 'string', description: 'HTML body (optional)' },
            },
            required: ['to', 'subject', 'body'],
        },
    },
    {
        name: 'list_emails',
        description: 'List received and sent emails.',
        inputSchema: {
            type: 'object',
            properties: {
                label: { type: 'string', description: 'Filter by label: inbox, sent, trash' },
                direction: { type: 'string', description: 'Filter: inbound or outbound' },
                limit: { type: 'number', description: 'Max results (default 20)' },
            },
        },
    },
    {
        name: 'get_email',
        description: 'Get the full content of a specific email by ID.',
        inputSchema: {
            type: 'object',
            properties: {
                message_id: { type: 'string', description: 'Email message ID' },
            },
            required: ['message_id'],
        },
    },
    {
        name: 'reply_email',
        description: 'Reply to an email. Automatically sets the recipient, and prefixes the subject with Re:.',
        inputSchema: {
            type: 'object',
            properties: {
                message_id: { type: 'string', description: 'ID of the email to reply to' },
                body: { type: 'string', description: 'Plain text reply body' },
                html: { type: 'string', description: 'HTML reply body (optional)' },
            },
            required: ['message_id', 'body'],
        },
    },
    {
        name: 'register_email',
        description: 'Register your email address with mailr. Called automatically on agent startup.',
        inputSchema: {
            type: 'object',
            properties: {
                address: { type: 'string', description: 'Full email address to register (e.g. agent@mail.example.com)' },
                label: { type: 'string', description: 'Label for the address (optional, defaults to agent ID)' },
            },
            required: ['address'],
        },
    },
    {
        name: 'dream',
        description: 'Run a memory maintenance cycle: consolidate related memories, decay old ones, prune low-importance entries, re-embed missing vectors, and generate a reflection. Use this when you notice your memories are getting cluttered or contradictory.',
        inputSchema: {
            type: 'object',
            properties: {},
        },
    },
    {
        name: 'log_chat',
        description: 'Log a chat message for daily review. Called automatically by the agent runtime.',
        inputSchema: {
            type: 'object',
            properties: {
                chat_id: { type: 'string', description: 'Conversation identifier' },
                role: { type: 'string', description: 'user or assistant' },
                content: { type: 'string', description: 'Message content' },
            },
            required: ['chat_id', 'role', 'content'],
        },
    },
    {
        name: 'send_notification',
        description: 'Queue a proactive notification to be sent to the user via their configured channel (e.g. Telegram). Use this for reminders, alerts, or anything the user should know about even when they\'re not in a conversation. Set deliver_at for scheduled delivery (e.g. "remind me in 10 minutes"), or omit it to send on the next check cycle (~5 minutes).',
        inputSchema: {
            type: 'object',
            properties: {
                message: { type: 'string', description: 'The notification message to send' },
                deliver_at: { type: 'string', description: 'ISO 8601 timestamp for when to deliver (e.g. 2026-04-04T01:30:00Z). Omit for immediate delivery.' },
            },
            required: ['message'],
        },
    },
    {
        name: 'configure_notifications',
        description: 'Set up the notification channel for this agent. Currently supports Telegram. The user must provide their bot token and chat ID.',
        inputSchema: {
            type: 'object',
            properties: {
                telegram_token: { type: 'string', description: 'Telegram Bot API token' },
                telegram_chat_id: { type: 'string', description: 'Telegram chat ID to send notifications to' },
            },
            required: ['telegram_token', 'telegram_chat_id'],
        },
    },
];

// --- Helpers ---
function isValidEmail(addr) {
    return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(addr);
}

// --- Tool execution ---
async function executeTool(name, args) {
    switch (name) {
        case 'memory_store':
            await post('/memory', { namespace: args.namespace, key: args.key, content: args.value });
            return `Stored: [${args.namespace}] ${args.key}`;

        case 'memory_recall': {
            const params = new URLSearchParams();
            if (args.namespace) params.set('namespace', args.namespace);
            if (args.query) params.set('q', args.query);
            params.set('limit', '20');
            const memories = await get(`/memory?${params}`);
            if (!memories || (Array.isArray(memories) && memories.length === 0)) return 'No memories found.';
            return JSON.stringify(memories, null, 2);
        }

        case 'create_document': {
            const doc = await post('/docs', { title: args.title, content: args.content, tags: args.tags || [] });
            return `Document created: ${args.title} (id: ${doc?.id || 'unknown'})`;
        }

        case 'search_documents': {
            const results = await get(`/search?q=${encodeURIComponent(args.query)}`);
            if (!results) return 'No results found.';
            return JSON.stringify(results, null, 2);
        }

        case 'add_knowledge': {
            await post('/facts', { subject: args.subject, predicate: args.predicate, object: args.object, confidence: args.confidence || 1.0 });
            return `Stored: ${args.subject} ${args.predicate} ${args.object}`;
        }

        case 'query_knowledge': {
            const params = new URLSearchParams();
            if (args.subject) params.set('subject', args.subject);
            if (args.predicate) params.set('predicate', args.predicate);
            const facts = await get(`/facts?${params}`);
            if (!facts || (Array.isArray(facts) && facts.length === 0)) return 'No facts found.';
            return JSON.stringify(facts, null, 2);
        }

        case 'create_task': {
            const body = { title: args.title, description: args.description || '' };
            if (args.assignee) body.assignee = args.assignee;
            if (args.priority) body.priority = args.priority;
            if (args.due_date) body.due_date = args.due_date;
            if (args.parent_id) body.parent_id = args.parent_id;
            if (args.tags) body.tags = args.tags;
            const task = await post('/tasks', body);
            const id = task?.id || 'unknown';
            return `Task created.\n  task_id: ${id}\n  title: ${args.title}\n  status: ${task?.status || 'open'}\nUse this exact task_id (${id}) with transition_task to update status.`;
        }

        case 'update_task': {
            const body = {};
            for (const key of ['title', 'description', 'assignee', 'priority', 'due_date', 'tags']) {
                if (args[key] !== undefined) body[key] = args[key];
            }
            const task = await patch(`/tasks/${args.task_id}`, body);
            return `Task ${args.task_id} updated (status: ${task?.status || 'unknown'})`;
        }

        case 'transition_task': {
            const task = await post(`/tasks/${args.task_id}/transition`, { status: args.status });
            return `Task ${args.task_id} transitioned to ${task?.status || args.status} (was: ${args.status === task?.status ? 'confirmed' : 'check task'})`;
        }

        case 'list_tasks': {
            const params = args.status ? `?status=${args.status}` : '';
            const tasks = await get(`/tasks${params}`);
            if (!tasks || (Array.isArray(tasks) && tasks.length === 0)) return 'No tasks found.';
            return JSON.stringify(tasks, null, 2);
        }

        case 'send_email': {
            const invalid = args.to.filter(a => !isValidEmail(a));
            if (invalid.length > 0) return `Error: Invalid email address(es): ${invalid.join(', ')}. Each address must be in the format user@domain.com`;
            if (args.cc) {
                const invalidCc = args.cc.filter(a => !isValidEmail(a));
                if (invalidCc.length > 0) return `Error: Invalid CC address(es): ${invalidCc.join(', ')}`;
            }
            const msg = await post('/email/send', {
                from: MAILR_ADDRESS,
                to: args.to,
                cc: args.cc || [],
                subject: args.subject,
                body: args.body,
                html: args.html || '',
            });
            return `Email sent (id: ${msg?.id || 'unknown'}) to ${args.to.join(', ')}`;
        }

        case 'list_emails': {
            const params = new URLSearchParams();
            if (args.label) params.set('label', args.label);
            if (args.direction) params.set('direction', args.direction);
            params.set('limit', String(args.limit || 20));
            const msgs = await get(`/email/messages?${params}`);
            if (!msgs || (Array.isArray(msgs) && msgs.length === 0)) return 'No emails found.';
            return JSON.stringify(msgs, null, 2);
        }

        case 'get_email': {
            const msg = await get(`/email/messages/${args.message_id}`);
            if (!msg) return 'Email not found.';
            return JSON.stringify(msg, null, 2);
        }

        case 'reply_email': {
            const original = await get(`/email/messages/${args.message_id}`);
            if (!original) return 'Original email not found.';
            const subject = original.subject?.startsWith('Re: ') ? original.subject : `Re: ${original.subject || ''}`;
            const to = [original.from];
            const msg = await post('/email/send', {
                from: MAILR_ADDRESS,
                to,
                cc: [],
                subject,
                body: args.body,
                html: args.html || '',
            });
            return `Reply sent (id: ${msg?.id || 'unknown'}) to ${to.join(', ')}`;
        }

        case 'register_email': {
            const result = await post('/email/register', { address: args.address, label: args.label || AGENT_ID || '' });
            return JSON.stringify(result, null, 2);
        }

        case 'dream': {
            const result = await post('/memory/dream', {}, 600000);
            return JSON.stringify(result, null, 2);
        }

        case 'log_chat': {
            await post('/memory/chat-log', { chat_id: args.chat_id, role: args.role, content: args.content });
            return 'Logged.';
        }

        case 'send_notification': {
            // Store as a pending notification memory — the notify loop picks it up
            // If deliver_at is set, store it as expires_at so the loop waits until that time
            const key = `pending-notification-${Date.now()}`;
            const mem = { namespace: '_system', key, content: args.message, importance: 0.9 };
            if (args.deliver_at) {
                mem.expires_at = args.deliver_at;
            }
            await post('/memory', mem);
            if (args.deliver_at) {
                return `Notification scheduled for ${args.deliver_at}. It will be delivered within ~5 minutes of that time.`;
            }
            return 'Notification queued. It will be delivered within the next few minutes.';
        }

        case 'configure_notifications': {
            await post('/memory', { namespace: '_system', key: 'notify-telegram-token', content: args.telegram_token, importance: 1.0 });
            await post('/memory', { namespace: '_system', key: 'notify-telegram-chat-id', content: args.telegram_chat_id, importance: 1.0 });
            return 'Telegram notifications configured. The system checks every 5 minutes for: overdue tasks, expiring reminders, and queued notifications.';
        }

        default:
            return `Unknown tool: ${name}`;
    }
}

// --- MCP stdio protocol ---
const rl = readline.createInterface({ input: process.stdin });

function send(msg) {
    const json = JSON.stringify(msg);
    process.stdout.write(`Content-Length: ${Buffer.byteLength(json)}\r\n\r\n${json}`);
}

let buffer = '';

process.stdin.on('data', (chunk) => {
    buffer += chunk.toString();

    while (true) {
        const headerEnd = buffer.indexOf('\r\n\r\n');
        if (headerEnd === -1) break;

        const header = buffer.substring(0, headerEnd);
        const match = header.match(/Content-Length:\s*(\d+)/i);
        if (!match) { buffer = buffer.substring(headerEnd + 4); continue; }

        const len = parseInt(match[1]);
        const bodyStart = headerEnd + 4;
        if (buffer.length < bodyStart + len) break;

        const body = buffer.substring(bodyStart, bodyStart + len);
        buffer = buffer.substring(bodyStart + len);

        try {
            handleMessage(JSON.parse(body));
        } catch(e) {
            process.stderr.write(`[aiprod-mcp] Parse error: ${e.message}\n`);
        }
    }
});

async function handleMessage(msg) {
    if (msg.method === 'initialize') {
        send({
            jsonrpc: '2.0', id: msg.id,
            result: {
                protocolVersion: '2024-11-05',
                serverInfo: { name: 'aiprod', version: '1.0.0' },
                capabilities: { tools: {} },
            },
        });
    } else if (msg.method === 'notifications/initialized') {
        // No response needed
    } else if (msg.method === 'tools/list') {
        send({
            jsonrpc: '2.0', id: msg.id,
            result: { tools: TOOLS },
        });
    } else if (msg.method === 'tools/call') {
        const { name, arguments: args } = msg.params;
        try {
            const result = await executeTool(name, args || {});
            send({
                jsonrpc: '2.0', id: msg.id,
                result: { content: [{ type: 'text', text: String(result) }] },
            });
        } catch(e) {
            send({
                jsonrpc: '2.0', id: msg.id,
                result: { content: [{ type: 'text', text: `Error: ${e.message}` }], isError: true },
            });
        }
    } else if (msg.id) {
        send({ jsonrpc: '2.0', id: msg.id, error: { code: -32601, message: 'Method not found' } });
    }
}

process.stderr.write('[aiprod-mcp] Server started\n');
