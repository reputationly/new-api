import json, glob, re, sys
from pathlib import Path

def load(d):
    out = {}
    for p in glob.glob(str(Path(d) / '*.json')):
        r = json.load(open(p))
        out[(r['id'], r['tier'])] = r
    return out

def ace_checks(t):
    lines = [l for l in t.split('\n') if l.strip()]
    cjk = re.findall(r'[\u4e00-\u9fff]', t)
    bpm = re.findall(r'(?i)\b\d{2,3}\s*(?:bpm|beats per minute)\b', t)
    key = re.findall(r'(?i)\b(?:[A-G](?:#|b)?\s*(?:major|minor))\b', t)
    quotes = t.count('"')
    return dict(lines=len(lines), chars=len(t), cjk=len(cjk), bpm=len(bpm), keysig=len(key), quotes=quotes)

def music3_checks(t):
    heads = ['### Global Metadata', '### Vocal Details', '### Arrangement']
    return dict(has3=sum(1 for h in heads if h in t), chars=len(t),
                cjk=len(re.findall(r'[\u4e00-\u9fff]', t)), heads_found=[h for h in heads if h in t])

def qwen_checks(t):
    m = re.search(r'\{.*\}', t, re.S)
    ok = False; keys = []
    if m:
        try:
            j = json.loads(m.group(0)); keys = sorted(j.keys()); ok = 'rewritten_prompt' in j
        except Exception:
            ok = False
    return dict(json_ok=ok, keys=keys, chars=len(t), cjk=len(re.findall(r'[\u4e00-\u9fff]', t)))

def report(d, kind, must=None):
    recs = load(d)
    ids = sorted({k[0] for k in recs})
    tiers = ['none', 'low', 'high']
    print('==== ', d)
    for cid in ids:
        row = [cid]
        for t in tiers:
            r = recs.get((cid, t))
            if not r: row.append(t + ':MISSING'); continue
            body = r.get('text') or ''
            if kind == 'ace': c = ace_checks(body)
            elif kind == 'music3': c = music3_checks(body)
            else: c = qwen_checks(body)
            row.append('%s: %.1fs %s' % (t, r['elapsed'], json.dumps(c, ensure_ascii=False)))
        print('\n   '.join(row))
    print()

if __name__ == '__main__':
    report('/tmp/rec/ace', 'ace')
    report('/tmp/rec/music3', 'music3')
    report('/tmp/rec/qwen', 'qwen')
