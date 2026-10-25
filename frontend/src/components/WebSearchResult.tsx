type SearchResult = { title: string; url: string; summary: string };

function safeSource(value: string): boolean {
  try {
    const url = new URL(value);
    return (
      (url.protocol === "https:" || url.protocol === "http:") &&
      !!url.hostname &&
      !url.username &&
      !url.password &&
      value.length <= 2048
    );
  } catch {
    return false;
  }
}

export function WebSearchResult({ content }: { content: string }) {
  let data: { status: string; message: string; results: SearchResult[] };
  try {
    data = JSON.parse(content);
    if (
      !data ||
      typeof data.status !== "string" ||
      typeof data.message !== "string" ||
      !Array.isArray(data.results) ||
      !data.results.every(
        (r) =>
          r &&
          typeof r.title === "string" &&
          typeof r.url === "string" &&
          typeof r.summary === "string",
      )
    )
      return <p>{content}</p>;
  } catch {
    return <p>{content}</p>;
  }
  return (
    <div className="web-search-result">
      <p>{data.message}</p>
      {data.results.slice(0, 5).map((result, index) => (
        <article key={index}>
          {safeSource(result.url) ? (
            <a href={result.url} target="_blank" rel="noopener noreferrer">
              {result.title}
            </a>
          ) : (
            <span>{result.title}</span>
          )}
          <p>{result.summary}</p>
        </article>
      ))}
    </div>
  );
}
