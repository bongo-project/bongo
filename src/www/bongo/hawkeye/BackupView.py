import bongo.dragonfly
import bongo.dragonfly.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler
from tarfile import TarInfo
import datetime

class BackupHandler(HawkeyeHandler):
    def NeedsAuth(self, rp):
        return True

    def index_GET(self, req, rp):
        today = datetime.date.today()
        self.SetVariable("set", today.strftime("%y%m%d"))
        return self.SendTemplate(req, rp, "index.tpl")

    def download_GET(self, req, rp):
        backup = ""
        req.content_type = "application/x-tar"
        for filename in ["manager", "avirus"]:
            fullname = "/config/%s" % filename
            content = self._getFile(req, fullname)
            tf = TarInfo(fullname)
            tf.size = len(content)
            req.write(tf.tobuf())
            req.write(content)
            zerobuf = 512 - (len(content) % 512)
            req.write("\0" * zerobuf)
        return None

    def index_POST(self, req, rp):
        if not req.form:
            return bongo.dragonfly.HTTP_UNAUTHORIZED
        
        if not req.form.has_key("command"):
            return bongo.dragonfly.HTTP_UNAUTHORIZED

        config = self._getManagerFile(req)
        if config.has_key("agents"):
            for agent in config["agents"]:
                if req.form.has_key(agent["name"]):
                    agent["enabled"] = True
                else:
                    agent["enabled"] = False
        else:
            self.SetVariable("message", "Unable to read config")

        self._setManagerFile(req, config)
        return self.index_GET(req, rp)

    def _getFile(self, req, filename):
        store = None
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            configfile = store.Read(filename)
            store.Quit()
        except:
            if store: 
                store.Quit()
            return None
        
        return configfile
