import os
import sys

import Cookie
import StringIO
import logging
import socket

import BongoFieldStorage

import bongo.Xpl as Xpl

class HttpServer:
    fqdn = socket.getfqdn()

    def __init__(self, port=80):
        self.server_hostname = "%s:%d" % (self.fqdn, port)
        self.server_port = port
    

class HttpRequest:
    """Make a SimpleHTTPRequestHandler look like a mod_python request"""
    log = logging.getLogger("Dragonfly")
    options = { "DragonflyUriRoot" : "/user",
                "HawkeyeTmplRoot" : None,      # determine this later
                "HawkeyeUriRoot" : "/admin"}

    def __init__(self, req, path, server=HttpServer()):
        self.oldreq = req

        self.uri = req.path
        self.headers_in = req.headers
        self.method = req.command

        idx = req.path.find("?")
        if (-1 == idx):
            self.args = ""
        else:
            self.uri, self.args = req.path.split("?", 1)

        self.user = None

        self.sent_bodyct = 0
        self.wbuf = StringIO.StringIO()

        self.headers_out = {}
        self.content_type = "text/plain"

        cookies = Cookie.SimpleCookie(req.headers.get("Cookie", None))
        self.cookies_in = {}
        for key,val in cookies.items():
            # lowercase all cookie names, for compatibility with apache
            self.cookies_in[key.lower()] = val
        self.cookies_out = Cookie.SimpleCookie()

        self.parsed_uri = ("http", None, None, None, server.fqdn, server.server_port, path, None, None)

        self.server = server

        binpath = os.path.abspath(os.path.dirname(sys.argv[0]))

        tmplPath = os.path.abspath(os.path.join(Xpl.DEFAULT_DATA_DIR, "htdocs/hawkeye"))
        if self.options["HawkeyeTmplRoot"] == None:
            self.options["HawkeyeTmplRoot"] = tmplPath

        if not binpath.startswith(Xpl.DEFAULT_LIBEXEC_DIR):
            tmplPath = os.path.abspath(os.path.join(os.getcwd(), "hawkeye"))

        self.form = None
        reqtype = self.headers_in.get("Content-Type", None)
        if reqtype and (reqtype.startswith("application/x-www-form-urlencoded")
                        or reqtype.startswith("multipart/form-data")):
            self.form = BongoFieldStorage.get_fieldstorage(self)


    def get_options(self):
        return self.options

    def flush(self):
        self.wbuf.flush()

    def read(self, *args, **kwargs):
        return self.oldreq.rfile.read(*args, **kwargs)

    def write(self, str, *args, **kwargs):
        self.sent_bodyct += len(str)
        return self.wbuf.write(str)
