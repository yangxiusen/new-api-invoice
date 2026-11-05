"""Loopback-only, synthetic New API auth boundary and reverse proxy for local UI tests.
No production configuration imports, database access, or real credentials.
Run the real Invoice service separately on 127.0.0.1:5301 against invoice_test_browser.
"""
import base64
import hashlib
import hmac
import http.client
import json
import secrets
import time
from http.cookies import SimpleCookie
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

KEY = secrets.token_bytes(32)

def enc(value):
    return base64.urlsafe_b64encode(json.dumps(value, separators=(',', ':')).encode()).decode().rstrip('=')

def token(uid):
    body = enc({'alg': 'HS256', 'typ': 'JWT'}) + '.' + enc({'iss': 'new-api', 'aud': ['new-api-dashboard'], 'token_use': 'access', 'sub': str(uid), 'sid': 'fixture-' + str(uid), 'uv': 1, 'sv': 1, 'exp': int(time.time()) + 900})
    return body + '.' + base64.urlsafe_b64encode(hmac.digest(KEY, body.encode(), 'sha256')).decode().rstrip('=')

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send_json(self, status, data, cookie=None):
        raw = json.dumps(data).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Cache-Control', 'no-store')
        if cookie:
            self.send_header('Set-Cookie', cookie)
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def handle_request(self):
        if self.path.startswith('/invoice/') or self.path.startswith('/invoice-api/'):
            conn = http.client.HTTPConnection('127.0.0.1', 5301, timeout=90)
            raw = self.rfile.read(int(self.headers.get('Content-Length', '0')))
            conn.request(self.command, self.path, raw, dict(self.headers))
            res = conn.getresponse()
            data = res.read()
            self.send_response(res.status)
            for key, value in res.getheaders():
                if key.lower() not in ('transfer-encoding', 'connection', 'content-length'):
                    self.send_header(key, value)
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            conn.close()
            return
        if self.path == '/api/user/self':
            raw = self.headers.get('Authorization', '').removeprefix('Bearer ')
            try:
                a, b, signature = raw.split('.')
                expected = base64.urlsafe_b64encode(hmac.digest(KEY, (a+'.'+b).encode(), 'sha256')).decode().rstrip('=')
                claims = json.loads(base64.urlsafe_b64decode(b + '=' * (-len(b) % 4)))
                uid = int(claims['sub'])
                if not hmac.compare_digest(signature, expected) or claims['exp'] <= time.time() or uid not in (1, 2, 3):
                    raise ValueError()
            except (ValueError, KeyError, TypeError):
                self.send_json(401, {'success': False})
                return
            self.send_json(200, {'success': True, 'data': {'id': uid, 'username': 'fixture', 'role': 100 if uid == 1 else 1, 'status': 1}})
            return
        cookie = SimpleCookie(self.headers.get('Cookie', ''))
        uid = cookie['invoice_fixture_user'].value if 'invoice_fixture_user' in cookie else '1'
        if self.path == '/api/user/auth/logout':
            self.send_json(200, {'success': True}, 'invoice_fixture_user=0; Path=/; SameSite=Strict; HttpOnly')
            return
        if self.path == '/api/user/auth/refresh':
            if uid not in ('1', '2', '3'):
                self.send_json(401, {'success': False, 'code': 'AUTH_UNAUTHORIZED'})
                return
            expected = self.headers.get('X-Auth-Session')
            if expected and expected != 'fixture-'+uid:
                self.send_json(409, {'success': False, 'code': 'AUTH_SESSION_MISMATCH'})
                return
            self.send_json(200, {'success': True, 'data': {'access_token': token(uid), 'token_type': 'Bearer', 'access_expires_at': int(time.time())+900, 'session': {'sid': 'fixture-'+uid}, 'user': {'id': int(uid)}}})
            return
        if self.path.startswith('/fixture-login/'):
            selected = self.path.rsplit('/', 1)[1]
            if selected not in ('1', '2', '3'):
                self.send_json(400, {'success': False})
                return
            self.send_response(302)
            self.send_header('Set-Cookie', 'invoice_fixture_user='+selected+'; Path=/; SameSite=Strict; HttpOnly')
            self.send_header('Location', '/invoice/applications')
            self.end_headers()
            return
        self.send_response(200)
        self.send_header('Content-Type', 'text/html; charset=utf-8')
        self.end_headers()
        self.wfile.write('<h1>仅本地测试 · 原站认证边界桩</h1><a href="/fixture-login/1">测试管理员</a> | <a href="/fixture-login/2">测试用户</a> | <a href="/fixture-login/3">未开通用户</a>'.encode())

    do_GET = handle_request
    do_POST = handle_request
    do_PUT = handle_request
    do_DELETE = handle_request

if __name__ == '__main__':
    print('Synthetic auth test boundary listening on http://127.0.0.1:5300', flush=True)
    ThreadingHTTPServer(('127.0.0.1', 5300), Handler).serve_forever()
