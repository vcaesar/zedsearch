// Run: node search.mjs (Node.js 18+, no dependencies)

const zedHost = process.env.ZED_HOST || "http://localhost:4080";
const user = process.env.ZED_USER || "admin";
const password = process.env.ZED_PASSWORD || "Complexpass#123";
const index = "games3";

// search_type: match, matchphrase, term, prefix, wildcard, fuzzy, querystring,
// matchall; optional query.start_time / query.end_time filter on @timestamp.
const params = {
  search_type: "match",
  query: { term: "DEMTSCHENKO", field: "_all" },
  max_results: 10,
};

// const params = {
//   search_type: "querystring",
//   query: { term: "+City:Turin +Silver" },
// };

const res = await fetch(`${zedHost}/api/${index}/_search`, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: "Basic " + Buffer.from(`${user}:${password}`).toString("base64"),
  },
  body: JSON.stringify(params),
});

const text = await res.text();
if (!res.ok) {
  throw new Error(`HTTP ${res.status}: ${text}`);
}
console.log(text);
