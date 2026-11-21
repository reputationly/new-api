import json, os, re, sys, time, base64, concurrent.futures as cf
from pathlib import Path
REPO = Path('/Users/reputationly/Desktop/code/api/new-api/.wtp')
sys.path.insert(0, '/Users/reputationly/Desktop/code/api/new-api/.h3exp/h3pe')
os.environ['MAAS_API_KEY'] = 'sk-80EwJPBlvmh2sGl6WrMZj9c4ZkM6GU2jUoRtjMV3TGzuXSdr'
import lib
MODEL = 'qwen3.8-flash-fp8'
def readf(p):
    return (REPO / p).read_text(encoding='utf-8')
QWEN_EDIT = readf('service/qwenpe/system_prompt_edit.txt').rstrip('\n')
QWEN_T2I = readf('service/qwenpe/system_prompt_t2i.txt').rstrip('\n')
ACESTEP = readf('service/acestep/ace_caption_v1.txt').rstrip('\n')
QWEN_ESCAPE = 'JSON escaping: the whole reply is parsed with a strict JSON parser. Every straight double quote that appears INSIDE a string value — including the quotes around text rendered in the image — must be escaped as \\" (for example: {"rewritten_prompt": "a sign that reads \\"OPEN\\""}). Never emit a bare " inside a value. The reply must begin with { and end with } — no code fences, no tags, no words before or after the object.\n\nAdditional rules (they refine, never override, the instructions above):\n- Orientation first. Before writing, decide the canvas from the subject\'s natural shape: labels, tables, menus, couplets, posters, phone screens, book pages and standing figures are vertical; landscapes, desks, storefronts and wide signs are horizontal; icons, dials and badges are square. Never fall back to 3:2 out of habit — pick it only when the subject is genuinely horizontal.\n- Commit. Give every object and every string of text one exact position and size. Never hedge with "or", "such as", "possibly", "或", "如", "可能" — decide, and state it as fact.\n- Content before finish. The opening sentence names the medium, style, subject and orientation. Then describe what is in the frame — subjects, layout, every text string in quotes — and only after that the lighting, materials and mood.\n- When several input images are combined into a new scene, there is no canvas: set wh_ratio to the ratio you chose and leave ratio_follow empty. ratio_follow is only for editing one existing picture.\n- Read the request narrowly. Change exactly the element the user named and nothing next to it (a "main title" is only the largest title line, not the subtitle). When the input contains readable text that must survive, list each of those strings verbatim in the preservation clause instead of summarising them.\n- Objects taken from an input image keep their own materials and colours exactly: clear glass stays clear, a liquid keeps its tint, a label keeps its text and layout. Scene lighting may add highlights, reflections and shadows on them, but it never recolours them — describe the object\'s own colour, then the light falling on it, as two separate facts.\n- When several objects are arranged together, give each its own non-overlapping spot on the surface with a concrete position ("upper left", "lower right", "centre"), resting flat or standing on the surface. Objects never stack on, lean against or overlap each other unless the user asks for that.\n- When editing text, replace only the characters the user changed; the label or words next to them (such as "电话" before a phone number) stay exactly where they are.'
QWEN_ANALYSIS = 'Think inside the reply. Make "analysis" the FIRST key of the JSON object, before rewritten_prompt:\n{"analysis": {"fixed": ["every text string the user wants shown, verbatim", "every named object, count, colour, position"], "canvas": "for edits: which input image is the canvas, what changes, what stays; for new scenes: none", "orientation": "vertical | horizontal | square — and the subject-based reason in ten words", "ratio": "the wh_ratio or ratio_follow you will output"}, "rewritten_prompt": "...", "wh_ratio": "...", "ratio_follow": "..."}\nKeep analysis under 80 words. It is discarded by the caller — only rewritten_prompt and the ratio fields are used — so decide everything in analysis first, then write rewritten_prompt and the ratio fields so they agree with it exactly.'
QWEN_CLOSING = ''

src = readf('service/aggregate_enhance_music.go')
i = src.find('const defaultMusic3EnhanceTemplate = `')
j = src.find('`', i + 40)
MUSIC3 = src[i + len('const defaultMusic3EnhanceTemplate = `'):j]
def instrumental(lyrics):
    for line in lyrics.split('\n'):
        rest = re.sub(r'\[[^\]]*\]', ' ', line)
        if re.search(r'[A-Za-z0-9\u4e00-\u9fff]', rest):
            return False
    return True
def tags_of(lyrics):
    return list(dict.fromkeys(re.findall(r'\[([^\]]+)\]', lyrics)))
def ctx_music3(caption, lyrics, budget):
    f = ['Task: text-to-music caption rewrite for MiniMax Music 3. Write the caption in English.']
    if not lyrics.strip(): f.append('No lyrics were supplied: treat this as an instrumental piece.')
    elif instrumental(lyrics): f.append('The lyrics contain only section tags and no sung words: treat this as an instrumental piece, and keep the section order below.')
    else: f.append('The lyrics below are for reference only; not one word of them may appear in the caption.')
    t = tags_of(lyrics)
    if t: f.append('Section tags present, in order: ' + ', '.join(t) + '. Build the Arrangement along exactly this order.')
    f.append('Caption budget: at most ' + str(budget) + ' characters.')
    return chr(10).join(f)
def ctx_ace(caption, lyrics, task, src_audio, budget):
    f = ['Task: ACE-Step music caption rewrite. Write the caption in English.']
    if task == 'cover':
        f.append('The task is a COVER: a source song is supplied and the target keeps its melody structure. Describe the target style - instrumentation, vocal treatment, production - not the source.')
    elif task == 'repaint':
        f.append('The task is a REPAINT: one region of a source audio is regenerated. Describe the style that region must land in, consistent with the surrounding material.')
    else:
        f.append('The task is text-to-music: the caption is the only guide for style.')
    if src_audio: f.append('A source audio file is attached to this request.')
    if not lyrics.strip(): f.append('No lyrics were supplied: treat this as an instrumental piece and describe no singer.')
    elif instrumental(lyrics): f.append('The lyrics contain only section tags and no sung words: treat this as instrumental, and keep the section order below.')
    else: f.append('The lyrics are for reference only; not one word of them may appear in the caption.')
    t = tags_of(lyrics)
    if t: f.append('Section tags present, in order: ' + ', '.join(t) + '. Your structure hints and energy description must follow exactly this order.')
    f.append('Caption budget: at most ' + str(budget) + ' characters. The current caption is ' + str(len(caption.strip())) + ' characters.')
    return chr(10).join(f)
LYR = chr(10) + chr(10) + '=== LYRICS (reference only - never quote, paraphrase, summarise, or translate) ===' + chr(10)
LYR_END = chr(10) + '=== END LYRICS ==='
def build(case):
    mode = case['mode']
    if mode == 'qwen_pe':
        imgs = case.get('images') or []
        if not imgs:
            system = QWEN_T2I + chr(10) + chr(10) + QWEN_ESCAPE + chr(10) + chr(10) + QWEN_ANALYSIS + chr(10)
        else:
            s0 = QWEN_EDIT
            if QWEN_CLOSING and s0.endswith(QWEN_CLOSING):
                head = s0[:-len(QWEN_CLOSING)].rstrip(chr(10))
                system = head + chr(10) + chr(10) + QWEN_ESCAPE + chr(10) + chr(10) + QWEN_CLOSING + chr(10)
            else:
                system = s0 + chr(10) + chr(10) + QWEN_ESCAPE + chr(10)
        parts = [{'type': 'text', 'text': case['prompt']}]
        r = case.get('ratio', 'adaptive')
        if r and r != 'adaptive':
            parts[0]['text'] += chr(10) + chr(10) + '(The output canvas is fixed by the caller at ' + r + ' - compose for it.)'
        for p in imgs:
            uri, _ = lib.prep_image(p)
            parts.append({'type': 'image_url', 'image_url': {'url': uri}})
        user = parts if len(parts) > 1 else case['prompt']
    else:
        if mode == 'music3':
            system = MUSIC3 + chr(10) + chr(10) + ctx_music3(case['prompt'], case.get('lyrics', ''), case.get('budget', 1500))
        else:
            system = ACESTEP + chr(10) + chr(10) + ctx_ace(case['prompt'], case.get('lyrics', ''), case.get('task', 't2m'), case.get('src', False), case.get('budget', 1000))
        user = case['prompt'] + (LYR + case['lyrics'] + LYR_END if case.get('lyrics') else '')
    return [{'role': 'system', 'content': system}, {'role': 'user', 'content': user}]
def kw(tier):
    if tier == 'none':
        return {'chat_template_kwargs': {'enable_thinking': False}}
    return {'reasoning_effort': tier}
def run(case, tier):
    msgs = build(case)
    t0 = time.time()
    st, body, el = lib.chat(MODEL, msgs, **kw(tier))
    rec = {'id': case['id'], 'mode': case['mode'], 'tier': tier, 'status': st, 'elapsed': round(el, 1)}
    if st != 200 or not isinstance(body, dict) or 'choices' not in body:
        rec['error'] = str(body)[:300]
    else:
        rec['text'] = body['choices'][0]['message'].get('content') or ''
        rec['usage'] = body.get('usage')
    return rec
def main():
    cases = json.load(open(sys.argv[1], encoding='utf-8'))
    outdir = Path(sys.argv[2]); outdir.mkdir(parents=True, exist_ok=True)
    tiers = sys.argv[3].split(',') if len(sys.argv) > 3 else ['none', 'low']
    jobs = [(c, t) for c in cases for t in tiers]
    with cf.ThreadPoolExecutor(6) as ex:
        futs = {ex.submit(run, c, t): (c, t) for c, t in jobs}
        for fu in cf.as_completed(futs):
            r = fu.result()
            json.dump(r, open(outdir / (r['id'] + '_' + r['tier'] + '.json'), 'w'), ensure_ascii=False, indent=1)
            print(r['id'], r['tier'], r['status'], r['elapsed'], len(r.get('text') or ''), flush=True)
main()