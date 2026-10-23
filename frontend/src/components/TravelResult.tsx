type Candidate = {
  id: string;
  name: string;
  price_fen: number | null;
  address?: string;
};
type Evidence = {
  status: string;
  message?: string;
  candidates?: Candidate[];
  source?: { provider?: string };
  weather?: { date?: string; description: string; temperature_c?: number }[];
};
type Plan = {
  revision: number;
  tasks: {
    id: string;
    origin: string;
    destination: string;
    departure_date: string;
    train?: { candidate: Candidate };
    hotel?: { candidate: Candidate };
  }[];
  validation?: { status: string; issues?: { code: string; message: string }[] };
};
export function TravelResult({ content }: { content: string }) {
  let data: {
    trip?: { plan: Plan };
    evidence?: unknown;
    status?: string;
    message?: string;
    reason?: string;
    key?: string;
    retracted?: boolean;
    revision?: number;
  };
  try {
    data = JSON.parse(content);
  } catch {
    return <p>工具已返回结果。</p>;
  }
  if (!data || typeof data !== "object") return <p>工具已返回结果。</p>;
  const plan = data.trip?.plan;
  const evidence: Evidence[] = Array.isArray(data.evidence)
    ? data.evidence
    : data.status
      ? [data as Evidence]
      : [];
  const price = (v: number | null) =>
    v == null ? "价格未核验" : `${(v / 100).toFixed(2)} 元`;
  return (
    <div className="travel-result">
      {plan?.tasks && (
        <>
          <p>行程已保存 · 修订 {plan.revision}</p>
          {plan.tasks.map((t) => (
            <div key={t.id}>
              <strong>
                {t.origin} → {t.destination}
              </strong>
              <p>{t.departure_date}</p>
              {t.train && (
                <p>
                  车次：{t.train.candidate.name} ·{" "}
                  {price(t.train.candidate.price_fen)}
                </p>
              )}
              {t.hotel && (
                <p>酒店地点：{t.hotel.candidate.name} · 房价与库存待确认</p>
              )}
            </div>
          ))}
          {plan.validation?.issues?.map((v, i) => (
            <p key={`${v.code}-${i}`}>待确认：{v.message}</p>
          ))}
        </>
      )}
      {evidence.map((q, i) => (
        <div key={i}>
          {q.source?.provider && <small>查询来源：{q.source.provider}</small>}
          {q.message && <p>{q.message}</p>}
          {q.status === "unavailable" && !q.message && <p>该查询暂不可用。</p>}
          {q.candidates?.slice(0, 5).map((c) => (
            <p key={c.id}>
              {c.name} · {price(c.price_fen)}
              {c.address ? ` · ${c.address}` : ""}
            </p>
          ))}
          {q.weather?.map((w, j) => (
            <p key={j}>
              {w.date} {w.description}
              {w.temperature_c == null ? "" : ` ${w.temperature_c}°C`}
            </p>
          ))}
        </div>
      ))}
      {data.reason && <p>{data.reason}</p>}
      {data.message && !data.status && <p>{data.message}</p>}
      {data.key && (
        <p>
          {data.retracted ? "已撤回偏好" : "已保存偏好"}：{data.key}
        </p>
      )}
      {plan && <small>规划和查询结果不代表已预订或付款。</small>}
    </div>
  );
}
