import os

from bongo.dragonfly.BongoPSP import PSP
from bongo.MDB import MDB
import bongo.dragonfly

class HawkeyeHandler:
    def __init__(self):
        pass

    def GetMdb(self, req):
        session = req.session
        return MDB(session.get("credUser"), session.get("credPass"))

    def NeedsAuth(self, rp):
        return True

    def ParsePath(self, rp):
        pass

    def index(self, req, rp):
        return bongo.dragonfly.OK

    def SendTemplate(self, req, rp, file, vars={}):
        path = os.path.join(rp.tmplPath, file)

        if os.path.exists(path):
            req.log.debug("sending template: %s", path)
        else:
            req.log.debug("template does not exist: %s", path)
            return bongo.dragonfly.HTTP_NOT_FOUND

        req.content_type = "text/html"

        pspvars = {"rp" : rp}
        pspvars.update(vars)

        psp = None
        try:
            psp = PSP(req, path, vars=pspvars)
            psp.run()
            return bongo.dragonfly.OK
        except ImportError, e:
            req.log.debug("Could not import psp from mod_python", exc_info=1)
            req.write("<html>")
            req.write("<h1>ERROR: %s</h1>" % str(e))
            req.write("<p>The standalone admin interface requires mod_python.  Maybe it is not installed?</p>")
            req.write("</html>")
            
            return bongo.dragonfly.HTTP_INTERNAL_SERVER_ERROR
        except:
            req.log.debug("Exception running psp", exc_info=1)
            if psp:
                psp.display_code()
            return bongo.dragonfly.HTTP_INTERNAL_SERVER_ERROR
