#!/usr/bin/env python

import os, os.path, stat, sys
import logging

import BaseHTTPServer
import email.Utils
import httplib
import time
import cStringIO as StringIO
import socket
import SocketServer
import types
from pprint import pformat
from SimpleHTTPServer import SimpleHTTPRequestHandler

import bongo.Privs as Privs
import bongo.Xpl as Xpl

import bongo.dragonfly
from bongo.dragonfly.ResourcePath import ResourcePath
from bongo.dragonfly.SmsHandler import SmsHandler
from bongo.dragonfly.HistoryHandler import HistoryHandler
from bongo.dragonfly.Standalone import HttpServer, HttpRequest
from bongo.dragonfly.HttpError import HttpError
import bongo.dragonfly.Auth
import bongo.dragonfly.BongoUtil
import bongo.dragonfly.BongoSession as BongoSession
import bongo.hawkeye.Auth
from bongo.hawkeye.HawkeyePath import HawkeyePath
from bongo.external.simplejson import loads, dumps
import Cookie

from optparse import OptionParser

KEY_FILE = "/etc/apache2/ssl.key/server.key"
CERT_FILE = "/etc/apache2/ssl.crt/server.crt"

class DragonflyHandler(SimpleHTTPRequestHandler):
    dragonfly_req = False

    def setup(self):
        self.connection = self.request
        self.rfile = socket._fileobject(self.request, "rb", self.rbufsize)
        self.wfile = socket._fileobject(self.request, "wb", self.wbufsize)
        self.server = HttpServer(options.port)

    def send_head(self):
        # copy the SimpleHTTPServerRequestHandler send_head, and make
        # sure we send a Last-Modified header.  It's too bad it
        # doesn't give us an easier way to do this.

        path = self.translate_path(self.path)
        f = None
        if os.path.isdir(path):
            for index in "index.html", "index.htm":
                index = os.path.join(path, index)
                if os.path.exists(index):
                    path = index
                    break
                else:
                    return self.list_directory(path)
        ctype = self.guess_type(path)

        if os.path.exists(path):
            mtime = os.path.getmtime(path)

            if os.path.islink(path):
                stats = os.lstat(path)
                lmtime = stats[stat.ST_MTIME]
                mtime = max(mtime, lmtime)

            if self.headers.has_key("If-Modified-Since"):
                since = self.headers["If-Modified-Since"]
                if since.find(";") != -1:
                    since = since.split(";", 1)[0]

                try:
                    msince = int(email.Utils.mktime_tz(email.Utils.parsedate_tz(since)))
                except:
                    msince = 0
                
                if not msince < mtime:
                    self.send_response(304)
                    self.end_headers()
                    return f

        try:
            # Always read in binary mode. Opening files in text mode may cause
            # newline translations, making the actual size of the content
            # transmitted *less* than the content-length!
            f = open(path, 'rb')
        except IOError:
            self.send_error(404, "File not found")
            return None
        self.send_response(200)

        self.send_header("Last-Modified", email.Utils.formatdate(mtime))
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(os.fstat(f.fileno())[6]))
        self.end_headers()
        return f

    def send_response(self, *args):
        try:
            SimpleHTTPRequestHandler.send_response(self, *args)
        except Exception, e:
            if type(e) is types.InstanceType:
                # only dragonfly requests have a logger, so we can't
                # just use self.req.log here.
                log = logging.getLogger("Dragonfly")
                log.error("Failed to send HTTP response: %s", e[1])
                return
            raise

        if self.dragonfly_req is False:
            return

        if not self.req.headers_out.has_key("Cache-Control"):
            self.req.headers_out["Cache-Control"] = "no-cache"

        for key,value in self.req.headers_out.items():
            self.req.oldreq.send_header(key, value)

        for morsel in self.req.cookies_out.values():
            self.req.oldreq.send_header("Set-Cookie",
                                        morsel.output(header='').lstrip())

        ct = self.req.content_type
        self.req.oldreq.send_header("Content-Type", ct)

        self.req.oldreq.end_headers()

        if ct in ("text/javascript"):
            resp = self.req.wbuf.getvalue()

            if options.debug and ct == "text/javascript":
                try:
                    resp = "\n" + pformat(loads(resp))
                except Exception, e:
                    resp = "\nException logging response: %s" % e
                    
            
            self.req.log.debug("Response (%s): %s" % (ct, resp))
        else:
            self.req.log.debug("Response (%s): unlogged data" % ct)
        self.req.oldreq.wfile.write(self.req.wbuf.getvalue())
        self.req.wbuf.close()

    def handle_one_request(self):
        self.raw_requestline = self.rfile.readline()
        if not self.raw_requestline:
            self.close_connection = 1
            return
        if not self.parse_request(): # An error code has been sent, just exit
            return

        if self.path.startswith("/sms"):
            handler = SmsHandler()
            mname = "do_" + self.command
            if not hasattr(handler, mname):
                self.send_error(501, "Unsupported method (%r)" % self.command)
                return
            method = getattr(handler, mname)
            self.dragonfly_req = True
            self.req = HttpRequest(self, "/sms", self.server)
            ret = method(self.req)

            if ret is None:
                self.send_response(bongo.dragonfly.HTTP_OK)
            else:
                self.send_response(ret)
            return

        if self.path.startswith("/history"):
            handler = HistoryHandler()
            mname = "do_" + self.command
            if not hasattr(handler, mname):
                self.send_error(501, "Unsupported method (%r)" % self.command)
                return
            method = getattr(handler, mname)
            self.dragonfly_req = True
            self.req = HttpRequest(self, "/history", self.server)
            ret = method(self.req)

            if ret is None:
                self.send_response(bongo.dragonfly.HTTP_OK)
            else:
                self.send_response(ret)
            return


        if self.path.startswith("/admin"):
            self.dragonfly_req = True
            req = HttpRequest(self, "/admin", self.server)
            self.req = req

            rp = HawkeyePath(req)
            handler = rp.GetHandler()

            req.session = BongoSession.Session(req)
            req.session.load()

            if handler.NeedsAuth(rp):
                auth = bongo.hawkeye.Auth.authenhandler(req)
                if auth != bongo.dragonfly.HTTP_OK:
                    target = "/admin/login?%s" % req.uri
                    self.send_response(
                        bongo.dragonfly.BongoUtil.redirect(req, target))
                    return

            req.log.debug("request for %s (handled by %s)", req.uri, handler)

            mname = rp.action + "_" + req.method

            if not hasattr(handler, mname):
                req.log.debug("%s has no %s", handler, mname)
                self.send_response(bongo.dragonfly.HTTP_NOT_FOUND)
                return

            method = getattr(handler, mname)
            ret = method(req, rp)

            if ret is None:
                self.send_response(bongo.dragonfly.HTTP_OK)
            else:
                self.send_response(ret)
            return

        if self.path.startswith("/dav"):            
            handler = DavHandler()
            print "running %s" % (self.command)
            mname = "do_" + self.command
            if not hasattr(handler, mname):
                self.send_error(501, "Unsupported method (%r)" % self.command)
                return
            method = getattr(handler, mname)
            self.dragonfly_req = True
            req = HttpRequest(self, "/dav", self.server)
            self.req = req
            auth = bongo.dragonfly.Auth.authenhandler(self.req)
            if auth != bongo.dragonfly.HTTP_OK:
                print "no auth"
                self.send_error(auth)
                return auth

            if req.user == None :
                req.headers_out["WWW-Authenticate"] = "Basic realm=\"Bongo\""
                self.send_error(bongo.dragonfly.HTTP_UNAUTHORIZED)
                return bongo.dragonfly.HTTP_UNAUTHORIZED
                
            print "user: %s" % (req.user)

            try:
                print "running method"
                ret = method(self.req)
            except HttpError, e:
                print "http error"
                self.send_error(e.code)
                return
            except Exception, e:
                req.log.exception("Unexpected error")
                print "error"
                self.send_error(bongo.dragonfly.HTTP_INTERNAL_SERVER_ERROR)
                return
            
            if ret is None:
                print "valid response!"
                self.send_response(bongo.dragonfly.HTTP_OK)
            else:
                self.send_response(ret)
            return

        if not self.path.startswith("/user"):
            if options.auth_always:
                auth = bongo.dragonfly.Auth.authenhandler(
                    HttpRequest(self, "/", self.server))
                if auth != bongo.dragonfly.HTTP_OK:
                    self.send_error(auth)
                    return auth

            mname = "do_" + self.command
            if not hasattr(self, mname):
                self.send_error(501, "Unsupported method (%r)" % self.command)
                return
            method = getattr(self, mname)
            method()
            return

        if options.sleep > 0:
            print "(sleeping for %d seconds...)" % options.sleep
            time.sleep(options.sleep)

        self.dragonfly_req = True
        req = HttpRequest(self, "/user", self.server)
        self.req = req

        auth = bongo.dragonfly.Auth.authenhandler(req)
        if auth != bongo.dragonfly.HTTP_OK:
            self.send_error(auth)
            return auth

        try:
            rp = ResourcePath(req)
            handler = rp.GetHandler()
        except HttpError, e:
            self.send_error(e.code)
            return 

        req.log.debug("request for %s (handled by %s)", req.uri, handler)
        command = req.headers_in.get("X-Bongo-HttpMethod", req.method)

        mname = "do_" + command
        if not hasattr(handler, mname):
            req.log.debug("%s has no %s", handler, mname)
            self.send_error(bongo.dragonfly.HTTP_NOT_IMPLEMENTED)
            return

        method = getattr(handler, mname)

        try:
            ret = method(req, rp)
        except HttpError, e:
            self.send_error(e.code)
            return
        except Exception, e:
            req.log.exception("Unexpected error")
            self.send_error(bongo.dragonfly.HTTP_INTERNAL_SERVER_ERROR)
            return

        if ret is None:
            req.log.error("Handler %s returned invalid value: None" % handler)
            self.send_response(bongo.dragonfly.OK)
        else:
            self.send_response(ret)

    def address_string(self):
        if options.no_dns:
            return self.client_address[0]

        host, port = self.client_address[:2]
        return socket.getfqdn(host)

class ThreadingHTTPServer(SocketServer.ThreadingMixIn,
                    SocketServer.TCPServer,
                    BaseHTTPServer.HTTPServer):
    allow_reuse_address = True

class ThreadingSSLServer(ThreadingHTTPServer):
    address_family = socket.AF_INET
    socket_type = socket.SOCK_STREAM
    request_queue_size = 5

    def __init__(self, server_address, HandlerClass) :
        SocketServer.BaseServer.__init__(self, server_address, HandlerClass)

        from OpenSSL import SSL
        ctx = SSL.Context(SSL.SSLv23_METHOD)
        ctx.use_privatekey_file(KEY_FILE)
        ctx.use_certificate_file(CERT_FILE)
        self.socket = SSL.Connection(ctx, socket.socket(self.address_family,
                                                        self.socket_type))
        self.server_bind()
        self.server_activate()

default_file_path = os.path.abspath(Xpl.DEFAULT_DATA_DIR+"/htdocs")

parser = OptionParser()
parser.add_option("-d", "--debug", dest="debug", action="store_true",
                  help="enable debugging output")
parser.add_option("-D", "--devel", dest="devel", action="store_true",
                  help="development mode (implies -d)")
parser.add_option("", "--profile", dest="profile", action="store_true",
                  help="enable profiling output")
parser.add_option("", "--profile-memory", dest="profile_memory", action="store_true",
                  help="enable memory profiling (requires sizer)")
parser.add_option("", "--ssl", dest="ssl", action="store_true",
                  help="enable ssl")
parser.add_option("-p", "--port", type="int", dest="port", default=8080,
                  help="specify the port for the http server [default: %default]")
parser.add_option("-s", "--sleep", type="int", dest="sleep", default=0,
                  help="specify the port for the http server [default: %default]")
parser.add_option("-a", "--auth-always", dest="auth_always", action="store_true",
                  help="authenticate all requests")
parser.add_option("", "--no-dns", dest="no_dns", action="store_true",
                  help="disable reverse dns lookups for logging")
parser.add_option("", "--file-path", dest="file_path",
                  default=default_file_path,
                  help="specify a static file path to serve")
(options, args) = parser.parse_args()

if __name__ == '__main__':
    formatter = logging.Formatter("%(levelname)s: %(message)s")
    console = logging.StreamHandler()
    console.setFormatter(formatter)
    logging.root.addHandler(console)

    if options.debug or options.devel:
        logging.root.setLevel(logging.DEBUG)
    else:
        logging.root.setLevel(logging.INFO)

    if os.geteuid() != 0:
        if Xpl.BONGO_USER is None:
            print "ERROR: %s must be run as root" % (os.path.basename(sys.argv[0]))
            sys.exit(1)

        uid, gid = Privs.GetBongoPwent()
        if os.geteuid() != uid:
            print "ERROR: %s must be run as root or your bongo user (%s)" \
                  % (os.path.basename(sys.argv[0]), Xpl.BONGO_USER)
            sys.exit(1)

    if options.devel:
        default_file_path = os.path.abspath(os.path.dirname(sys.argv[0]))
        HttpRequest.options["HawkeyeTmplRoot"] =  "%s/%s" % (default_file_path, "hawkeye")
        print "Devel: serving static files from %s" % default_file_path

    if options.file_path:
        httppath = options.file_path

    # SimpleServer serves static files from cwd
    os.chdir(httppath)

    proto = "http"
    if options.ssl:
        proto = "https"
        httpd = ThreadingSSLServer(("", options.port), DragonflyHandler)
    else:
        httpd = ThreadingHTTPServer(("", options.port), DragonflyHandler)

    # drop privs, now that we've opened the port and read the ssl cert
    Privs.DropPrivs()

    print "serving at %s://%s:%s/dragonfly.html" % (proto, socket.getfqdn(), options.port)

    try:
        if options.profile_memory:
            from sizer import scanner
            try:
                httpd.serve_forever()
            except KeyboardInterrupt:
                import code
                objs = scanner.Objects()
                code.interact(local = {'objs' : objs})
        if options.profile:
            import hotshot, hotshot.stats, test.pystone
            prof = hotshot.Profile(cwd+"/bongo.prof")
            benchtime, stones = prof.runcall(httpd.serve_forever)
            prof.close()
        else:
            httpd.serve_forever()
    except KeyboardInterrupt:
        if options.profile:
            prof.close()
