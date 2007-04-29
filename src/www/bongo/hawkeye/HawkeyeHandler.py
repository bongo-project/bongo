import os
from bongo.Template import Template
import bongo.dragonfly

class HawkeyeHandler:
    def __init__(self):
        self._template = Template()

    def NeedsAuth(self, rp):
        return True

    def ParsePath(self, rp):
        pass

    def SetVariable(self, name, value):
        self._template.setVariable(name, value)

    def index(self, req, rp):
        return bongo.dragonfly.OK

    def SendTemplate(self, req, rp, file):
        path = os.path.join(rp.tmplPath, file)
        req.content_type = "text/html"

        if os.path.exists(path):
            print "sending template: %s / %s" % (rp.tmplPath, file)
            req.log.debug("sending template: %s", path)
        else:
            print "template does not exist: %s / %s " % (rp.tmplPath, file)
            req.log.debug("template does not exist: %s", path)
            req.write("<html><body><h1>Error</h1><p>Template not found.</p></body></html>")
            return bongo.dragonfly.HTTP_NOT_FOUND

        self.SetVariable("message", "")
        try:
            t = self._template
            t.setTemplatePath(rp.tmplRoot)
            t.setTemplateUriRoot(rp.tmplUriRoot)
            t.setTemplateFile(path)
            t.Run(req)
            return bongo.dragonfly.OK
        except ImportError, e:
            req.write("<html><h1>ERROR</h1><p>%s</p></body></html>" % str(e))
            return bongo.dragonfly.HTTP_INTERNAL_SERVER_ERROR
        #except:
        #    print "Error running Template"
        #    req.write("<html><body><h1>Error</h1><p>Very bad error!</p></body></html>")
        #    return bongo.dragonfly.HTTP_INTERNAL_SERVER_ERROR
