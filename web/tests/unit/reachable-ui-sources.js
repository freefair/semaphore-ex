import fs from 'fs';
import path from 'path';
import { parseNoPatch as parse } from 'babel-eslint';

// Check the selected application graph; unreferenced upstream compatibility
// components are not product UI, but become subject to the gate if imported.
export default function reachableUISources(sourceRoot) {
  const visited = new Set();
  const visit = (filename) => {
    if (visited.has(filename) || filename.startsWith(`${path.join(sourceRoot, 'lang')}${path.sep}`)) return;
    visited.add(filename);
    const source = fs.readFileSync(filename, 'utf8');
    const script = filename.endsWith('.vue')
      ? (source.match(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/) || [null, ''])[1]
      : source;
    const ast = parse(script, { sourceType: 'module', ecmaVersion: 2020 });
    const resolveImport = (specifier) => {
      if (!specifier.startsWith('.') && !specifier.startsWith('@/')) return;
      const base = specifier.startsWith('@/')
        ? path.join(sourceRoot, specifier.slice(2))
        : path.resolve(path.dirname(filename), specifier);
      const target = [base, `${base}.js`, `${base}.vue`, path.join(base, 'index.js'), path.join(base, 'index.vue')]
        .find((candidate) => fs.existsSync(candidate) && fs.statSync(candidate).isFile());
      if (!target) throw new Error(`Unresolved local UI import: ${specifier} in ${filename}`);
      if (/\.(js|vue)$/.test(target)) visit(target);
    };
    const walk = (node) => {
      if (!node || typeof node !== 'object') return;
      if (['ImportDeclaration', 'ExportNamedDeclaration', 'ExportAllDeclaration'].includes(node.type)
          && node.source) resolveImport(node.source.value);
      if (node.type === 'CallExpression'
          && (node.callee.type === 'Import' || node.callee.name === 'require')
          && ['Literal', 'StringLiteral'].includes(node.arguments[0]?.type)) resolveImport(node.arguments[0].value);
      if (node.type === 'ImportExpression' && ['Literal', 'StringLiteral'].includes(node.source.type)) {
        resolveImport(node.source.value);
      }
      const loadingCall = node.type === 'CallExpression'
        && (node.callee.type === 'Import' || node.callee.name === 'require');
      if (loadingCall && !['Literal', 'StringLiteral'].includes(node.arguments[0]?.type)) {
        throw new Error(`Dynamic UI import requires an explicit inventory: ${filename}`);
      }
      if (node.type === 'ImportExpression'
          && !['Literal', 'StringLiteral'].includes(node.source.type)) {
        throw new Error(`Dynamic UI import requires an explicit inventory: ${filename}`);
      }
      if (node.type === 'CallExpression' && node.callee.type === 'MemberExpression'
          && node.callee.object.name === 'require' && node.callee.property.name === 'context') {
        throw new Error(`Context UI imports require an explicit inventory: ${filename}`);
      }
      Object.values(node).forEach((value) => {
        if (Array.isArray(value)) value.forEach(walk);
        else if (value && typeof value === 'object') walk(value);
      });
    };
    walk(ast.program || ast);
  };
  visit(path.join(sourceRoot, 'main.js'));
  return [...visited];
}
