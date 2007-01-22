import os
import sys
import BongoFieldStorage
import BongoSession
from cgi import escape

def path_split(filename):
    dir, fname = os.path.split(filename)
    if sys.platform.startswith("win"):
        dir += "\\"
    else:
        dir += "/"

    return dir, fname

class BongoPSPInterface:
    def __init__(self, req, filename, form):
        self.req = req
        self.filename = filename
        self.error_page = None
        self.form = form

class BongoPSP:
    def __init__(self, req, filename=None, string=None, vars={}):
        from mod_python import _psp

        if (string and filename):
            raise ValueError, "Must specify either filename or string"

        self.req, self.vars = req, vars

        if not filename and not string:
            filename = req.filename

        self.filename, self.string = filename, string

        if filename:
            if not os.path.isabs(filename):
                base = os.path.split(req.filename)[0]
                self.filename = os.path.join(base, filename)

            self.load_from_file()
        else:
            self.code = _psp.parsestring(string)

    def load_from_file(self):
        from mod_python import _psp

        filename = self.filename

        if not os.path.isfile(filename):
            raise ValueError, "%s is not a file" % filename

        dir, fname = path_split(self.filename)
        source = _psp.parse(fname, dir)
        self.code = compile(source, filename, "exec")

    def run(self, vars={}):
        code, req = self.code, self.req

        # does this code use session?
        session = None
        if "session" in code.co_names:
            session = req.session

        # does this code use form?
        form = None
        if "form" in code.co_names:
            form = req.form

        psp = BongoPSPInterface(req, self.filename, form)

        try:
            global_scope = globals().copy()
            global_scope.update({"req":req, "session":session,
                                 "form":form, "psp":psp})
            global_scope.update(self.vars)
            global_scope.update(vars)
            try:
                exec code in global_scope
                req.flush()

                if session is not None:
                    session.save()
            except:
                et, ev, etb = sys.exc_info()
                if psp.error_page:
                    # run error page
                    psp.error_page.run({"exception": (et, ev, etb)})
                else:
                    raise et, ev, etb
        finally:
            if session is not None:
                session.unlock()

    def display_code(self):
        from mod_python import _psp

        req, filename = self.req, self.filename

        dir, fname = path_split(filename)

        source = open(filename).read().splitlines()
        pycode = _psp.parse(fname, dir).splitlines()

        source = [s.rstrip() for s in source]
        pycode = [s.rstrip() for s in pycode]

        req.write("<table>\n<tr>")
        for s in ("", "&nbsp;PSP-produced Python Code:",
                  "&nbsp;%s:" % filename):
            req.write("<td><tt>%s</tt></td>" % s)
        req.write("</tr>\n")

        n = 1
        for line in pycode:
            req.write("<tr>")
            left = escape(line).replace("\t", " "*4).replace(" ", "&nbsp;")
            if len(source) < n:
                right = ""
            else:
                right = escape(source[n-1]).replace("\t", " "*4).replace(" ", "&nbsp;")
            for s in ("%d.&nbsp;" % n,
                      "<font color=blue>%s</font>" % left,
                      "&nbsp;<font color=green>%s</font>" % right):
                req.write("<td><tt>%s</tt></td>" % s)
            req.write("</tr>\n")

            n += 1
        req.write("</table>\n")

try:
    from mod_python import _apache
    from mod_python import psp
    PSP = psp.PSP
except ImportError:
    PSP = BongoPSP
