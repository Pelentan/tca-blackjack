/**
 * Document Service
 * Language: TypeScript · Node.js
 * Library: PDFKit
 *
 * Why TypeScript/Node? Document generation is a presentation layer problem.
 * PDFKit is the gold standard for programmatic PDF generation in Node.
 * Consistent with auth-service — TypeScript across presentation-layer services.
 *
 * Pure layout engine — no domain knowledge. Callers shape their data into
 * blocks (text, table, footer). This service renders them.
 *
 * Endpoints:
 *   POST /document  — generate PDF, returns binary
 *   GET  /health    — service health + counter
 */

import http from 'http';
import PDFDocument from 'pdfkit';

// ── Types ─────────────────────────────────────────────────────────────────────

interface TextBlock  { text: string; }
interface FooterBlock { footer: string; }
interface TableBlock {
  table: {
    name: string;
    headers: string[];
    rows: string[];
  };
}
type Block = { text: string } | { footer: string } | { table: TableBlock['table'] };

interface DocumentRequest {
  caller:       string;
  title:        string;
  logo?:        string;       // UUID — reserved, not implemented
  heading?:     string;
  sub_heading?: string;
  blocks:       Block[];
  presentation?: unknown;     // Reserved, not implemented
}

// ── Config ────────────────────────────────────────────────────────────────────

const PORT = parseInt(process.env['PORT'] ?? '3011', 10);

const CALLER_ALLOWLIST = new Set([
  'bank-service',
  'game-state',
  'auth-service',
]);

// Defaults — will be wired from presentation block when implemented
const DEFAULTS = {
  font:      'Helvetica',
  primary:   '#1f6feb',
  secondary: '#8b949e',
  text:      '#24292f',
  pageSize:  'Letter' as const,
};

// ── In-memory counter ─────────────────────────────────────────────────────────

let documentsGenerated = 0;

// ── Request parsing ───────────────────────────────────────────────────────────

function readBody(req: http.IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on('data', chunk => chunks.push(chunk));
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

function sendJson(res: http.ServerResponse, status: number, body: unknown): void {
  const json = JSON.stringify(body);
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Access-Control-Allow-Origin': '*',
  });
  res.end(json);
}

function sendError(res: http.ServerResponse, status: number, code: string, message: string): void {
  sendJson(res, status, { error: { code, message } });
}

// ── Validation ────────────────────────────────────────────────────────────────

function validateRequest(body: unknown): { valid: true; req: DocumentRequest } | { valid: false; code: string; message: string } {
  if (typeof body !== 'object' || body === null) {
    return { valid: false, code: 'missing_required_fields', message: 'Request body must be a JSON object' };
  }
  const b = body as Record<string, unknown>;

  if (!b['caller'] || typeof b['caller'] !== 'string') {
    return { valid: false, code: 'missing_required_fields', message: 'caller is required' };
  }
  if (!CALLER_ALLOWLIST.has(b['caller'])) {
    return { valid: false, code: 'unknown_caller', message: `Caller '${b['caller']}' is not authorized` };
  }
  if (!b['title'] || typeof b['title'] !== 'string') {
    return { valid: false, code: 'missing_required_fields', message: 'title is required' };
  }
  if (!Array.isArray(b['blocks']) || b['blocks'].length === 0) {
    return { valid: false, code: 'missing_required_fields', message: 'blocks array is required and must not be empty' };
  }

  // Validate each block
  for (let i = 0; i < b['blocks'].length; i++) {
    const block = b['blocks'][i] as Record<string, unknown>;
    const keys = Object.keys(block);
    if (keys.length !== 1) {
      return { valid: false, code: 'invalid_block', message: `Block ${i}: must have exactly one key (text, table, or footer)` };
    }
    const type = keys[0];
    if (type === 'text') {
      if (typeof block['text'] !== 'string') {
        return { valid: false, code: 'invalid_block', message: `Block ${i}: text must be a string` };
      }
    } else if (type === 'footer') {
      if (typeof block['footer'] !== 'string') {
        return { valid: false, code: 'invalid_block', message: `Block ${i}: footer must be a string` };
      }
    } else if (type === 'table') {
      const t = block['table'] as Record<string, unknown>;
      if (!t || typeof t !== 'object') {
        return { valid: false, code: 'invalid_block', message: `Block ${i}: table must be an object` };
      }
      if (!Array.isArray(t['headers']) || t['headers'].length === 0) {
        return { valid: false, code: 'invalid_block', message: `Block ${i}: table.headers must be a non-empty array` };
      }
      if (!Array.isArray(t['rows'])) {
        return { valid: false, code: 'invalid_block', message: `Block ${i}: table.rows must be an array` };
      }
      // Validate row column counts
      const colCount = (t['headers'] as unknown[]).length;
      for (let r = 0; r < (t['rows'] as unknown[]).length; r++) {
        const row = (t['rows'] as string[])[r];
        if (typeof row !== 'string') {
          return { valid: false, code: 'invalid_block', message: `Block ${i}: table row ${r} must be a CSV string` };
        }
        const cols = parseCSVRow(row);
        if (cols.length !== colCount) {
          return { valid: false, code: 'invalid_table_rows', message: `Block ${i}: table row ${r} has ${cols.length} columns, expected ${colCount}` };
        }
      }
    } else {
      return { valid: false, code: 'invalid_block', message: `Block ${i}: unknown block type '${type}'` };
    }
  }

  return { valid: true, req: b as unknown as DocumentRequest };
}

// ── CSV parsing (RFC 4180) ────────────────────────────────────────────────────

function parseCSVRow(row: string): string[] {
  const cols: string[] = [];
  let current = '';
  let inQuotes = false;
  for (let i = 0; i < row.length; i++) {
    const ch = row[i];
    if (ch === '"') {
      if (inQuotes && row[i + 1] === '"') { current += '"'; i++; }
      else { inQuotes = !inQuotes; }
    } else if (ch === ',' && !inQuotes) {
      cols.push(current.trim());
      current = '';
    } else {
      current += ch;
    }
  }
  cols.push(current.trim());
  return cols;
}

// ── PDF Rendering ─────────────────────────────────────────────────────────────

function renderPDF(req: DocumentRequest): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    const doc = new PDFDocument({
      size: DEFAULTS.pageSize,
      margins: { top: 50, bottom: 60, left: 60, right: 60 },
      info: { Title: req.title, Creator: 'TCA Blackjack · document-service' },
    });

    doc.on('data', chunk => chunks.push(chunk));
    doc.on('end', () => resolve(Buffer.concat(chunks)));
    doc.on('error', reject);

    // ── Header ──────────────────────────────────────────────────────────────
    // logo: reserved — would embed image here when implemented

    doc.font(DEFAULTS.font + '-Bold')
       .fontSize(20)
       .fillColor(DEFAULTS.primary)
       .text(req.title, { align: 'left' });

    if (req.heading) {
      doc.moveDown(0.3)
         .font(DEFAULTS.font + '-Bold')
         .fontSize(13)
         .fillColor(DEFAULTS.text)
         .text(req.heading);
    }

    if (req.sub_heading) {
      doc.moveDown(0.2)
         .font(DEFAULTS.font)
         .fontSize(11)
         .fillColor(DEFAULTS.secondary)
         .text(req.sub_heading);
    }

    doc.moveDown(0.5)
       .moveTo(doc.page.margins.left, doc.y)
       .lineTo(doc.page.width - doc.page.margins.right, doc.y)
       .strokeColor(DEFAULTS.secondary)
       .lineWidth(0.5)
       .stroke();

    doc.moveDown(0.8);

    // ── Blocks ───────────────────────────────────────────────────────────────
    let footerText: string | null = null;

    for (const block of req.blocks) {
      if ('text' in block) {
        doc.font(DEFAULTS.font)
           .fontSize(10)
           .fillColor(DEFAULTS.text)
           .text(block.text, { align: 'left' })
           .moveDown(0.6);

      } else if ('footer' in block) {
        footerText = block.footer; // Collected, rendered after all blocks

      } else if ('table' in block) {
        const { name, headers, rows } = block.table;

        // Table name
        if (name) {
          doc.font(DEFAULTS.font + '-Bold')
             .fontSize(10)
             .fillColor(DEFAULTS.secondary)
             .text(name.toUpperCase(), { characterSpacing: 0.5 })
             .moveDown(0.3);
        }

        const tableWidth = doc.page.width - doc.page.margins.left - doc.page.margins.right;
        const rowHeight = 18;
        const headerHeight = 20;
        const pageBottom = doc.page.height - doc.page.margins.bottom - 10;

        // Proportional column widths: last column gets extra space for timestamps etc.
        // Default: equal. If last col header is 'Timestamp' give it 1.8x, compress others.
        const lastIsWide = headers[headers.length - 1]?.toLowerCase() === 'timestamp';
        const colWidths: number[] = (() => {
          if (!lastIsWide || headers.length < 2) {
            return headers.map(() => tableWidth / headers.length);
          }
          const wideShare = 1.8;
          const normalCount = headers.length - 1;
          const unit = tableWidth / (normalCount + wideShare);
          return headers.map((_, i) => i === headers.length - 1 ? unit * wideShare : unit);
        })();
        const colX = (i: number) => colWidths.slice(0, i).reduce((a, b) => a + b, doc.page.margins.left);

        // Helper: render a header row at given y
        const renderHeader = (y: number) => {
          doc.rect(doc.page.margins.left, y, tableWidth, headerHeight)
             .fill(DEFAULTS.primary);
          doc.font(DEFAULTS.font + '-Bold').fontSize(9).fillColor('#ffffff');
          headers.forEach((header, i) => {
            doc.text(header, colX(i) + 6, y + 5, { width: colWidths[i] - 12, lineBreak: false });
          });
        };

        let tableTop = doc.y;
        renderHeader(tableTop);
        let currentY = tableTop + headerHeight;
        // Track segments for border drawing: [{startY, rowCount}]
        const segments: { startY: number; rowCount: number }[] = [{ startY: tableTop, rowCount: 0 }];

        const parsedRows = rows.map(parseCSVRow);
        parsedRows.forEach((cols) => {
          // Page break if this row won't fit
          if (currentY + rowHeight > pageBottom) {
            // Close current segment border
            const seg = segments[segments.length - 1];
            const segHeight = headerHeight + seg.rowCount * rowHeight;
            doc.rect(doc.page.margins.left, seg.startY, tableWidth, segHeight)
               .strokeColor(DEFAULTS.secondary).lineWidth(0.5).stroke();
            for (let i = 1; i < headers.length; i++) {
              const x = colX(i);
              doc.moveTo(x, seg.startY).lineTo(x, seg.startY + segHeight)
                 .strokeColor(DEFAULTS.secondary).lineWidth(0.3).stroke();
            }

            doc.addPage();
            tableTop = doc.page.margins.top;
            renderHeader(tableTop);
            currentY = tableTop + headerHeight;
            segments.push({ startY: tableTop, rowCount: 0 });
          }

          const seg = segments[segments.length - 1];
          const rowIdx = seg.rowCount;
          if (rowIdx % 2 === 0) {
            doc.rect(doc.page.margins.left, currentY, tableWidth, rowHeight).fill('#f6f8fa');
          }
          doc.font(DEFAULTS.font).fontSize(9).fillColor(DEFAULTS.text);
          cols.forEach((col, i) => {
            doc.text(col, colX(i) + 6, currentY + 4, { width: colWidths[i] - 12, lineBreak: false });
          });
          seg.rowCount++;
          currentY += rowHeight;
        });

        // Close final segment
        const lastSeg = segments[segments.length - 1];
        const lastSegHeight = headerHeight + lastSeg.rowCount * rowHeight;
        doc.rect(doc.page.margins.left, lastSeg.startY, tableWidth, lastSegHeight)
           .strokeColor(DEFAULTS.secondary).lineWidth(0.5).stroke();
        for (let i = 1; i < headers.length; i++) {
          const x = colX(i);
          doc.moveTo(x, lastSeg.startY).lineTo(x, lastSeg.startY + lastSegHeight)
             .strokeColor(DEFAULTS.secondary).lineWidth(0.3).stroke();
        }

        doc.y = currentY + 12;
      }
    }

    // ── Footer ───────────────────────────────────────────────────────────────
    const footerY = doc.page.height - doc.page.margins.bottom;
    doc.moveTo(doc.page.margins.left, footerY - 8)
       .lineTo(doc.page.width - doc.page.margins.right, footerY - 8)
       .strokeColor(DEFAULTS.secondary)
       .lineWidth(0.5)
       .stroke();

    if (footerText) {
      doc.font(DEFAULTS.font)
         .fontSize(8)
         .fillColor(DEFAULTS.secondary)
         .text(footerText, doc.page.margins.left, footerY - 2, {
           width: doc.page.width - doc.page.margins.left - doc.page.margins.right,
           align: 'center',
         });
    }

    // Generated timestamp — always present
    const generated = `Generated ${new Date().toISOString()}`;
    doc.font(DEFAULTS.font)
       .fontSize(7)
       .fillColor(DEFAULTS.secondary)
       .text(generated, doc.page.margins.left, footerY + 8, {
         width: doc.page.width - doc.page.margins.left - doc.page.margins.right,
         align: 'right',
       });

    doc.end();
  });
}

// ── HTTP Server ───────────────────────────────────────────────────────────────

const server = http.createServer(async (req, res) => {
  const method = req.method ?? 'GET';
  const url = req.url ?? '/';

  // CORS preflight
  if (method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type',
    });
    res.end();
    return;
  }

  // GET /health
  if (method === 'GET' && url === '/health') {
    sendJson(res, 200, {
      status: 'healthy',
      service: 'document-service',
      language: 'TypeScript',
      library: 'PDFKit',
      documents_generated: documentsGenerated,
    });
    return;
  }

  // POST /document
  if (method === 'POST' && url === '/document') {
    let parsed: unknown;
    try {
      const body = await readBody(req);
      parsed = JSON.parse(body);
    } catch {
      sendError(res, 400, 'missing_required_fields', 'Request body must be valid JSON');
      return;
    }

    const validation = validateRequest(parsed);
    if (!validation.valid) {
      sendError(res, 400, validation.code, validation.message);
      return;
    }

    try {
      const pdfBuffer = await renderPDF(validation.req);
      const filename = `${validation.req.title.replace(/[^a-z0-9]/gi, '-').toLowerCase()}-${Date.now()}.pdf`;
      documentsGenerated++;
      res.writeHead(200, {
        'Content-Type': 'application/pdf',
        'Content-Disposition': `attachment; filename="${filename}"`,
        'Content-Length': pdfBuffer.length,
        'Access-Control-Allow-Origin': '*',
      });
      res.end(pdfBuffer);
      console.log(`[document-service] Generated: ${filename} (${pdfBuffer.length} bytes) for ${validation.req.caller}`);
    } catch (err) {
      console.error('[document-service] Render failed:', err);
      sendError(res, 500, 'render_failed', 'PDF generation failed');
    }
    return;
  }

  sendError(res, 404, 'not_found', `${method} ${url} not found`);
});

server.listen(PORT, () => {
  console.log(`[document-service] TypeScript · PDFKit · port ${PORT}`);
  console.log('[document-service] Layout engine ready — no domain knowledge here.');
});
