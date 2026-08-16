// A/B Comparison Test: Pipeline vs Harness
// 用法:
//   node e2e-ab-test.mjs                    # 运行所有场景
//   node e2e-ab-test.mjs --scenario writing       # 只运行全新写作场景
//   node e2e-ab-test.mjs --scenario guided        # 只运行引导式写作场景
//   node e2e-ab-test.mjs --scenario polish        # 只运行修改场景
//   node e2e-ab-test.mjs --scenario chat           # 只运行对话场景
//
// 环境变量:
//   WS_URL=ws://localhost:8080/api/v2/ws/agent  (默认)
//   AB_DELAY=10000  (两组之间延迟，默认 10s，避免 429)

import WebSocket from 'ws';
import { writeFileSync } from 'fs';

const WS_URL = process.env.WS_URL || 'ws://localhost:8080/api/v2/ws/agent';
const HTTP_URL = process.env.HTTP_URL || 'http://localhost:8080/api/v2';
const ADMIN_TOKEN = process.env.ADMIN_TOKEN || 'change-this-in-production';
const AB_DELAY = parseInt(process.env.AB_DELAY || '10000', 10);
const TIMEOUT = 180000; // 3 min per run

// ── 获取 JWT token（使用 admin API key 登录） ──────────
let cachedToken = null;

async function getToken() {
  if (cachedToken) return cachedToken;
  try {
    const resp = await fetch(`${HTTP_URL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ api_key: ADMIN_TOKEN }),
    });
    if (!resp.ok) {
      console.warn(`⚠️  Admin login failed (${resp.status}), falling back to anonymous`);
      return null;
    }
    const data = await resp.json();
    cachedToken = data.data?.token || data.token || null;
    if (cachedToken) {
      console.log(`🔐 Admin token acquired (user: ${data.data?.user_id || data.user_id || 'admin'})`);
    }
    return cachedToken;
  } catch (err) {
    console.warn(`⚠️  Admin login error: ${err.message}, falling back to anonymous`);
    return null;
  }
}

function buildWsUrl(token) {
  if (!token) return WS_URL;
  const sep = WS_URL.includes('?') ? '&' : '?';
  return `${WS_URL}${sep}token=${encodeURIComponent(token)}`;
}

// ── 测试场景 ──────────────────────────────────────────
const SCENARIOS = [
  {
    name: 'writing',
    label: '全新写作（有搜索）',
    message: '基于热搜写一篇关于外卖骑手闯红灯的评论',
    style: 'yinyue',
    mode: 'auto',
  },
  {
    name: 'guided',
    label: '引导式写作（提纲确认）',
    message: '写一篇关于AI取代程序员焦虑的深度评论',
    style: 'yinyue',
    mode: 'guided',
  },
  {
    name: 'polish',
    label: '多轮修改（润色+缩短）',
    // 两轮: 第一轮写作, 第二轮修改
    multiTurn: true,
    turns: [
      { message: '写一篇关于年轻人躺平现象的评论', style: 'yinyue', mode: 'auto' },
      { message: '把文章润色一下，语言更生动', style: 'yinyue', mode: 'auto' },
    ],
  },
  {
    name: 'chat',
    label: '纯对话（无写作）',
    message: '什么是印月三谈？',
    style: 'yinyue',
    mode: 'auto',
  },
];

// ── 单次运行 ──────────────────────────────────────────
function runSingle(wsUrl, { message, style, mode }, label, agentMode, sessionId, token) {
  const fullUrl = token ? buildWsUrl(token) : wsUrl;
  return new Promise((resolve) => {
    const ws = new WebSocket(fullUrl);
    const startTime = Date.now();
    let firstDeltaTime = null; // TTFT
    let streamChunks = 0;
    let fullArticle = '';
    let reasoningChunks = 0;
    let fullReasoning = '';
    let stepHistory = [];
    let stepStarted = {};
    let tokenUsage = null;
    let reviewResult = null;
    let error = null;
    let articleTitle = '';
    let traceId = '';

    const timer = setTimeout(() => {
      error = `timeout after ${TIMEOUT / 1000}s`;
      try { ws.close(); } catch {}
      resolve(buildMetrics());
    }, TIMEOUT);

    function buildMetrics() {
      clearTimeout(timer);
      const totalMs = Date.now() - startTime;
      const ttftMs = firstDeltaTime ? firstDeltaTime - startTime : null;
      return {
        label,
        totalMs,
        ttftMs,
        streamChunks,
        articleLength: fullArticle.length,
        reasoningLength: fullReasoning.length,
        reasoningChunks,
        stepCount: stepHistory.length,
        stepSequence: stepHistory.map(s => s.step),
        tokenUsage,
        reviewPassed: reviewResult?.passed ?? null,
        issueCount: reviewResult?.issues?.length ?? null,
        articleTitle,
        articleExcerpt: fullArticle.slice(0, 200),
        fullArticle,
        traceId,
        error,
      };
    }

    ws.on('open', () => {
      const payload = { message, style: style || 'yinyue', mode: mode || 'auto' };
      if (agentMode) payload.agent_mode = agentMode;
      if (sessionId) payload.session_id = sessionId;
      ws.send(JSON.stringify({ type: 'agent.start', payload }));
    });

    ws.on('message', (data) => {
      const msg = JSON.parse(data.toString());
      const { type, payload } = msg;

      switch (type) {
        case 'agent.created':
          traceId = payload?.trace_id || '';
          break;

        case 'agent.step.start':
          stepHistory.push({ step: payload.step, status: 'running' });
          stepStarted[payload.step] = Date.now();
          break;

        case 'agent.step.complete':
          {
            const rec = stepHistory.find(s => s.step === payload.step && s.status === 'running');
            if (rec) rec.status = 'complete';
          }
          break;

        case 'agent.reasoning':
          if (reasoningChunks === 0) firstDeltaTime = firstDeltaTime || Date.now();
          fullReasoning += payload.delta;
          reasoningChunks++;
          break;

        case 'agent.stream':
          if (streamChunks === 0 && firstDeltaTime === null) {
            firstDeltaTime = Date.now();
          }
          fullArticle += payload.delta;
          streamChunks++;
          break;

        case 'agent.stream.reset':
          // Pipeline agent loop may reset intermediate content before final output.
          // Track reset count for diagnostics but don't lose the final article.
          fullArticle = '';
          streamChunks = 0;
          break;

        case 'agent.stream.done':
          // If we didn't receive any stream deltas (e.g. Pipeline agent loop
          // where content was buffered in title parser), use the full_text payload.
          if (!fullArticle && payload?.full_text) {
            fullArticle = payload.full_text;
            streamChunks = streamChunks || 1;
          }
          break;

        case 'agent.await_input':
          // 自动确认提纲
          setTimeout(() => {
            try {
              ws.send(JSON.stringify({
                type: 'agent.confirm',
                payload: {
                  trace_id: payload.trace_id,
                  step: payload.step,
                  data: payload.data,
                },
              }));
            } catch {}
          }, 500);
          break;

        case 'agent.completed':
          reviewResult = payload.review || null;
          tokenUsage = payload.token_usage || null;
          articleTitle = payload.title || '';
          try { ws.close(); } catch {}
          resolve(buildMetrics());
          break;

        case 'agent.error':
          error = `[${payload.code}] ${payload.message}`;
          try { ws.close(); } catch {}
          resolve(buildMetrics());
          break;

        case 'agent.cancelled':
          error = 'cancelled';
          try { ws.close(); } catch {}
          resolve(buildMetrics());
          break;
      }
    });

    ws.on('error', (err) => {
      error = `ws error: ${err.message}`;
      resolve(buildMetrics());
    });

    ws.on('close', () => {
      resolve(buildMetrics());
    });
  });
}

// ── 多轮运行（用于 polish 场景） ──────────────────────
async function runMultiTurn(wsUrl, turns, label, agentMode, token) {
  const results = [];
  // 用同一 conversation_id 实现多轮
  // 但 WebSocket 每次是独立连接，所以用 trace 前缀标记
  let sessionId = null;
  for (let i = 0; i < turns.length; i++) {
    const turnLabel = `${label} - turn ${i + 1}/${turns.length}`;
    const result = await runSingle(wsUrl, turns[i], turnLabel, agentMode, sessionId, token);
    // Pass the first turn's trace_id as session_id for subsequent turns
    if (i === 0 && result.traceId) {
      sessionId = result.traceId;
    }
    results.push(result);
  }
  // 合并指标
  const totalMs = results.reduce((s, r) => s + r.totalMs, 0);
  const totalTokens = results.reduce((s, r) => s + (r.tokenUsage?.total_tokens || 0), 0);
  const finalArticle = results[results.length - 1]?.fullArticle || '';
  return {
    label,
    multiTurn: true,
    turns: results,
    totalMs,
    ttftMs: results[0]?.ttftMs,
    totalTokens,
    articleLength: finalArticle.length,
    articleExcerpt: finalArticle.slice(0, 200),
    articleTitle: results[results.length - 1]?.articleTitle || '',
    error: results.find(r => r.error)?.error,
  };
}

// ── 运行一个场景的 A/B 对比 ────────────────────────────
async function runScenarioAB(scenario, token) {
  console.log(`\n${'═'.repeat(60)}`);
  console.log(`📋 Scenario: ${scenario.name} — ${scenario.label}`);
  console.log(`${'═'.repeat(60)}\n`);

  const pipelineResult = await runScenarioSingle(scenario, 'pipeline', token);
  
  console.log(`\n⏳ Waiting ${AB_DELAY / 1000}s before harness run...`);
  await new Promise(r => setTimeout(r, AB_DELAY));

  const harnessResult = await runScenarioSingle(scenario, 'harness', token);

  // 对比
  console.log(`\n${'─'.repeat(60)}`);
  console.log(`📊 ${scenario.name} 对比结果:`);
  console.log(`${'─'.repeat(60)}\n`);

  const rows = [
    ['指标', 'Pipeline', 'Harness'],
    ['─'.repeat(16), '─'.repeat(16), '─'.repeat(16)],
  ];

  if (scenario.multiTurn) {
    rows.push(['总耗时 (ms)', String(pipelineResult.totalMs), String(harnessResult.totalMs)]);
    rows.push(['总 Token', String(pipelineResult.totalTokens), String(harnessResult.totalTokens)]);
    rows.push(['最终文章长度', String(pipelineResult.articleLength), String(harnessResult.articleLength)]);
    rows.push(['首轮 TTFT (ms)', String(pipelineResult.ttftMs || 'N/A'), String(harnessResult.ttftMs || 'N/A')]);
    rows.push(['错误', pipelineResult.error || '无', harnessResult.error || '无']);
  } else {
    rows.push(['总耗时 (ms)', String(pipelineResult.totalMs), String(harnessResult.totalMs)]);
    rows.push(['TTFT (ms)', String(pipelineResult.ttftMs || 'N/A'), String(harnessResult.ttftMs || 'N/A')]);
    rows.push(['Token', String(pipelineResult.tokenUsage?.total_tokens || 'N/A'), String(harnessResult.tokenUsage?.total_tokens || 'N/A')]);
    rows.push(['文章长度', String(pipelineResult.articleLength), String(harnessResult.articleLength)]);
    rows.push(['推理长度', String(pipelineResult.reasoningLength), String(harnessResult.reasoningLength)]);
    rows.push(['步骤数', String(pipelineResult.stepCount), String(harnessResult.stepCount)]);
    rows.push(['步骤序列', pipelineResult.stepSequence.join('→'), harnessResult.stepSequence.join('→')]);
    rows.push(['审校通过', String(pipelineResult.reviewPassed), String(harnessResult.reviewPassed)]);
    rows.push(['问题数', String(pipelineResult.issueCount ?? 'N/A'), String(harnessResult.issueCount ?? 'N/A')]);
    rows.push(['错误', pipelineResult.error || '无', harnessResult.error || '无']);
  }

  // 打印表格
  const colWidths = [20, 22, 22];
  for (const row of rows) {
    const line = row.map((cell, i) => String(cell).padEnd(colWidths[i])).join(' | ');
    console.log(`  ${line}`);
  }

  // 分析
  console.log();
  if (pipelineResult.error && harnessResult.error) {
    console.log('  ❌ 两种模式都出错');
  } else if (pipelineResult.error) {
    console.log('  ⚠️  Pipeline 出错，Harness 成功');
  } else if (harnessResult.error) {
    console.log('  ⚠️  Harness 出错，Pipeline 成功');
  } else {
    // TTFT 对比
    if (pipelineResult.ttftMs && harnessResult.ttftMs) {
      const diff = pipelineResult.ttftMs - harnessResult.ttftMs;
      const pct = ((diff / pipelineResult.ttftMs) * 100).toFixed(1);
      console.log(`  TTFT: Harness 比 Pipeline ${diff > 0 ? '快' : '慢'} ${Math.abs(diff)}ms (${Math.abs(pct)}%)`);
    }
    // Token 对比
    const pTokens = scenario.multiTurn ? pipelineResult.totalTokens : pipelineResult.tokenUsage?.total_tokens;
    const hTokens = scenario.multiTurn ? harnessResult.totalTokens : harnessResult.tokenUsage?.total_tokens;
    if (pTokens && hTokens) {
      const diff = pTokens - hTokens;
      const pct = ((diff / pTokens) * 100).toFixed(1);
      console.log(`  Token: Harness 比 Pipeline ${diff > 0 ? '少' : '多'} ${Math.abs(diff)} (${Math.abs(pct)}%)`);
    }
    // 总耗时
    const diff = pipelineResult.totalMs - harnessResult.totalMs;
    const pct = ((diff / pipelineResult.totalMs) * 100).toFixed(1);
    console.log(`  总耗时: Harness 比 Pipeline ${diff > 0 ? '快' : '慢'} ${Math.abs(diff)}ms (${Math.abs(pct)}%)`);
  }

  return { scenario: scenario.name, pipeline: pipelineResult, harness: harnessResult };
}

async function runScenarioSingle(scenario, agentMode, token) {
  const modeLabel = agentMode === 'pipeline' ? '📦 Pipeline' : '⚡ Harness';
  console.log(`\n  ${modeLabel} 运行中...`);

  if (scenario.multiTurn) {
    return await runMultiTurn(WS_URL, scenario.turns, `${scenario.name} (${agentMode})`, agentMode, token);
  } else {
    return await runSingle(WS_URL, scenario, `${scenario.name} (${agentMode})`, agentMode, undefined, token);
  }
}

// ── 主函数 ─────────────────────────────────────────────
async function main() {
  const args = process.argv.slice(2);
  const scenarioIdx = args.indexOf('--scenario');
  const scenarioFilter = scenarioIdx >= 0 ? args[scenarioIdx + 1] : null;

  const scenarios = scenarioFilter
    ? SCENARIOS.filter(s => s.name === scenarioFilter)
    : SCENARIOS;

  if (scenarios.length === 0) {
    console.error(`No matching scenario: ${scenarioFilter}`);
    console.error(`Available: ${SCENARIOS.map(s => s.name).join(', ')}`);
    process.exit(1);
  }

  // 获取 admin token
  const token = await getToken();

  console.log(`\n🧪 A/B Test: Pipeline vs Harness`);
  console.log(`   WS: ${WS_URL}`);
  console.log(`   Auth: ${token ? 'admin JWT' : 'anonymous (no token)'}`);
  console.log(`   Scenarios: ${scenarios.map(s => s.name).join(', ')}`);
  console.log(`   Delay between A/B: ${AB_DELAY / 1000}s\n`);

  const allResults = [];
  for (const scenario of scenarios) {
    try {
      const result = await runScenarioAB(scenario, token);
      allResults.push(result);
    } catch (err) {
      console.error(`Scenario ${scenario.name} failed: ${err.message}`);
      allResults.push({ scenario: scenario.name, error: err.message });
    }
  }

  // 最终汇总
  console.log(`\n${'═'.repeat(60)}`);
  console.log(`📈 最终汇总`);
  console.log(`${'═'.repeat(60)}\n`);

  const summaryRows = [
    ['场景', 'Pipeline 耗时', 'Harness 耗时', 'Pipeline Token', 'Harness Token', '胜者'],
    ['─'.repeat(12), '─'.repeat(12), '─'.repeat(12), '─'.repeat(14), '─'.repeat(14), '─'.repeat(8)],
  ];

  for (const r of allResults) {
    if (r.error) {
      summaryRows.push([r.scenario, 'ERROR', 'ERROR', '-', '-', '-']);
      continue;
    }
    const pMs = r.pipeline?.totalMs ?? 'N/A';
    const hMs = r.harness?.totalMs ?? 'N/A';
    const pTok = r.multiTurn ? r.pipeline?.totalTokens : r.pipeline?.tokenUsage?.total_tokens;
    const hTok = r.multiTurn ? r.harness?.totalTokens : r.harness?.tokenUsage?.total_tokens;
    let winner = '-';
    if (pMs !== 'N/A' && hMs !== 'N/A') {
      if (r.pipeline?.error && !r.harness?.error) winner = 'Harness';
      else if (!r.pipeline?.error && r.harness?.error) winner = 'Pipeline';
      else if (pMs < hMs) winner = 'Pipeline';
      else winner = 'Harness';
    }
    summaryRows.push([
      r.scenario,
      typeof pMs === 'number' ? `${pMs}ms` : String(pMs),
      typeof hMs === 'number' ? `${hMs}ms` : String(hMs),
      pTok ? String(pTok) : 'N/A',
      hTok ? String(hTok) : 'N/A',
      winner,
    ]);
  }

  const colW = [14, 14, 14, 16, 16, 10];
  for (const row of summaryRows) {
    const line = row.map((cell, i) => String(cell).padEnd(colW[i])).join(' | ');
    console.log(`  ${line}`);
  }

  // 保存结果到 JSON
  const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
  const outFile = `ab-test-results-${timestamp}.json`;
  writeFileSync(outFile, JSON.stringify(allResults, null, 2));
  console.log(`\n💾 Results saved to ${outFile}`);

  console.log('\n✅ A/B test complete.\n');
}

main().catch(err => {
  console.error('Fatal error:', err);
  process.exit(1);
});
