export const EXPR_STRING_LITERAL = /"(?:\\[\s\S]|[^"\\])*"|'(?:\\[\s\S]|[^'\\])*'/;

const SIMPLE_ESCAPES = {
  a: '\x07',
  b: '\b',
  f: '\f',
  n: '\n',
  r: '\r',
  t: '\t',
  v: '\v',
  '\\': '\\',
  "'": "'",
  '"': '"',
  '\x60': '\x60',
  '?': '?',
};

export function decodeExprStringLiteral(literal) {
  return literal
    .slice(1, -1)
    .replace(/\r\n?/g, '\n')
    .replace(
      /\\(?:u\{([\da-fA-F]{1,6})\}|[xX]([\da-fA-F]{2})|u([\da-fA-F]{4})|U([\da-fA-F]{8})|([0-3][0-7]{2})|([\s\S]))/g,
      (_, shortUnicode, hex, unicode, longUnicode, octal, simple) => {
        if (simple !== undefined) {
          if (!Object.hasOwn(SIMPLE_ESCAPES, simple)) {
            throw new Error('Invalid expression string escape');
          }
          return SIMPLE_ESCAPES[simple];
        }
        const code = octal
          ? parseInt(octal, 8)
          : parseInt(shortUnicode || hex || unicode || longUnicode, 16);
        return code >= 0xd800 && code <= 0xdfff
          ? '\ufffd'
          : String.fromCodePoint(code);
      },
    );
}
