export type OrderTraceEvent = {
  step: string;
  pod: string;
  at: string;
  requestId?: string;
  riderId?: string;
};

const traceSteps: Record<string, { label: string; describe: (rider: string) => string }> = {
  offer_created: { label: "创建派单邀请", describe: (rider) => `匹配到${rider}，创建派单邀请。` },
  offer_sent: { label: "发送 WebSocket 邀请", describe: (rider) => `持有${rider}的连接，通过 WebSocket 发出邀请。` },
  offer_declined: { label: "记录拒单", describe: (rider) => `记录${rider}的拒单，并释放为该邀请预留的接单名额。` },
  offer_released: { label: "释放派单邀请", describe: (rider) => `清除派单邀请，并释放为${rider}预留的接单名额。` },
  assigned: { label: "确认分配", describe: (rider) => `处理${rider}的接单回复，将订单分配给该骑手。` },
  completed: { label: "完成订单", describe: (rider) => `记录${rider}完成订单，释放该订单占用的接单名额。` },
};

const timeFormat = new Intl.DateTimeFormat("zh-CN", {
  hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
});

function TraceTime({ at }: { at: string }) {
  return <time dateTime={at} className="font-mono text-xs tabular-nums text-slate-500">{timeFormat.format(new Date(at))}</time>;
}

function TraceStep({ label, description, at, pod, podLabel, requestId }: {
  label: string;
  description?: string;
  at?: string;
  pod: string;
  podLabel: string;
  requestId?: string;
}) {
  return (
    <li className="space-y-1 border-l-2 border-slate-200 pl-3 text-sm">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        {at && <TraceTime at={at} />}
        <span className="font-medium text-slate-900">{label}</span>
      </div>
      <p className="leading-relaxed text-slate-700">
        <span title={pod || undefined} className="break-all font-mono text-xs font-medium">{pod ? `Pod ${podLabel}` : "Pod 未记录"}</span>
        {description && <span className="ml-2">{description}</span>}
        {requestId && <span title={requestId} className="ml-2 whitespace-nowrap font-mono text-xs text-slate-500">requestId = {requestId.slice(0, 8)}</span>}
      </p>
    </li>
  );
}

export function OrderDispatchTrace({ receivedPod, createdAt, trace = [] }: {
  receivedPod?: string;
  createdAt?: string;
  trace?: OrderTraceEvent[];
}) {
  const events = [...trace].sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  const claims = events.filter(({ step }) => step === "claimed");
  const stages = events.filter(({ step }) => step !== "claimed");
  const pods = [...new Set([receivedPod, ...events.map(({ pod }) => pod)])].filter((pod): pod is string => Boolean(pod));
  const claimCounts = new Map<string, number>();
  for (const { pod } of claims) {
    if (pod) claimCounts.set(pod, (claimCounts.get(pod) ?? 0) + 1);
  }

  const offerCreators = new Map<string, string>();
  for (const event of stages) {
    if (event.step === "offer_created" && event.requestId && event.pod) {
      offerCreators.set(event.requestId, event.pod);
    }
  }

  const crossPodRoutes = new Map<string, { from: string; to: string; count: number }>();
  for (const event of stages) {
    if (event.step !== "offer_sent" || !event.requestId || !event.pod) continue;
    const from = offerCreators.get(event.requestId);
    if (!from || from === event.pod) continue;
    const key = JSON.stringify([from, event.pod]);
    const route = crossPodRoutes.get(key);
    if (route) route.count++;
    else crossPodRoutes.set(key, { from, to: event.pod, count: 1 });
  }

  function podLabel(pod: string) {
    const short = pod.split("-").at(-1);
    return short && pods.filter((name) => name.split("-").at(-1) === short).length === 1 ? short : pod;
  }

  return (
    <div className="space-y-3">
      {pods.length > 0 && (
        <div className="space-y-2 rounded-lg bg-slate-50 p-3 text-sm">
          <p className="font-medium text-slate-900">参与 Pod <span className="tabular-nums">{pods.length}</span> 个</p>
          <ul className="flex flex-wrap gap-2">
            {pods.map((pod) => (
              <li key={pod} title={pod} className="break-all rounded border border-slate-200 bg-white px-2 py-1 text-xs">
                <span className="font-mono font-medium">{podLabel(pod)}</span>
                {pod === receivedPod && <span className="ml-2 text-slate-600">接收订单</span>}
                {claimCounts.has(pod) && <span className="ml-2 text-slate-600">领单 {claimCounts.get(pod)} 次</span>}
              </li>
            ))}
          </ul>
          {crossPodRoutes.size > 0 ? (
            <div className="rounded-md bg-emerald-50 px-3 py-2 text-emerald-900">
              <p className="font-medium">已确认跨 Pod 投递</p>
              <ul className="mt-1 space-y-1 text-xs">
                {[...crossPodRoutes.entries()].map(([key, { from, to, count }]) => (
                  <li key={key} className="break-all">
                    创建邀请 <span className="font-mono font-semibold" title={from}>{podLabel(from)}</span>
                    <span className="mx-2" aria-hidden="true">→</span>
                    WebSocket 投递 <span className="font-mono font-semibold" title={to}>{podLabel(to)}</span> · {count} 次
                  </li>
                ))}
              </ul>
            </div>
          ) : (
            <p className="text-xs text-slate-600">
              {claimCounts.size > 1 ? "多个 Pod 先后领单，当前记录暂无跨 Pod 投递。" : "当前记录暂无跨 Pod 投递。"}
            </p>
          )}
        </div>
      )}

      {stages.length === 0 && !receivedPod ? (
        <p className="text-sm text-slate-500">{claims.length ? "暂未记录派单邀请。" : "暂无服务端调度事件。"}</p>
      ) : (
        <ol className="max-h-96 list-none space-y-3 overflow-y-auto" aria-label="订单调度步骤">
          {receivedPod && (
            <TraceStep label="接收订单" description="把订单写入共享 MongoDB，等待调度。"
              at={createdAt} pod={receivedPod} podLabel={podLabel(receivedPod)} />
          )}
          {stages.map((event, index) => {
            const step = traceSteps[event.step];
            const rider = event.riderId ? `骑手 ${event.riderId} ` : "骑手";
            return (
              <TraceStep key={`${event.at}-${event.step}-${index}`} label={step?.label ?? event.step}
                description={step?.describe(rider) ?? (event.riderId ? `骑手 ${event.riderId}` : undefined)}
                at={event.at} pod={event.pod} podLabel={podLabel(event.pod)} requestId={event.requestId} />
            );
          })}
        </ol>
      )}

      {claims.length > 0 && (
        <details className="text-sm text-slate-600">
          <summary className="cursor-pointer">领单明细（{claims.length} 次）</summary>
          <ol className="mt-2 max-h-48 list-none space-y-1 overflow-y-auto pl-4" aria-label="领单明细">
            {claims.map(({ at, pod }, index) => (
              <li key={`${at}-${index}`} className="flex flex-wrap gap-x-3">
                <TraceTime at={at} />
                <span title={pod} className="break-all font-mono text-xs">{pod ? podLabel(pod) : "Pod 未记录"}</span>
              </li>
            ))}
          </ol>
        </details>
      )}
    </div>
  );
}
