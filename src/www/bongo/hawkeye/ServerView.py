import bongo.dragonfly
import bongo.dragonfly.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler

doneop = 0

class ServerHandler(HawkeyeHandler):
    def NeedsAuth(self, rp):
        return True

    def index_GET(self, req, rp):
        global doneop
        agentlist = []
        config = self._getManagerFile(req)
        
        if doneop:
            self.SetVariable("opsuccess", 1)
            doneop = 0
        else:
            self.SetVariable("opsuccess", 0)

        # change things which eval. to False to empty elements
        if config != None and config.has_key("agents"):
            for agent in config["agents"]:
               if not agent["enabled"]:
                   agent["enabled"] = None
            self.SetVariable("agentlist", config["agents"])
            self.SetVariable("success", 1)
        else:
            self.SetVariable("success", 0)
            self.SetVariable("error", "Unable to read configuration information from the Bongo store. Are you logged in as a user with administrative permissions?")

        self.SetVariable("breadcrumb", "Agents &#187 Enable/Disable Agents")
        self.SetVariable("title", "Enable/Disable Agents")
        self.SetVariable("agntab", "selecteditem")

        return self.SendTemplate(req, rp, "index.tpl")

    def index_POST(self, req, rp):
        global doneop
        
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
            self.SetVariable("error", "Unable to read config")

        self._setManagerFile(req, config)
        # Operation completed OK.
        doneop = 1
        return self.index_GET(req, rp)

    def login_POST(self, req, rp):
        if not Auth.Login(req):
            return self.login_GET(req, rp)

        target = rp.tmplUriRoot

        if req.args and len(req.args) > 0:
            target = req.args

        return bongo.dragonfly.BongoUtil.redirect(req, target)

    def _getManagerFile(self, req):
        config = {}
        store = None
        decoder = simplejson.JSONDecoder()
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            configfile = store.Read("/config/manager")
            config = decoder.decode(configfile)
            store.Quit()
        except:
            if store: 
                store.Quit()
            return None
        
        return config

    def _setManagerFile(self, req, obj):
        encoder = simplejson.JSONEncoder()
        store = None
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            configfile = encoder.encode(obj)
            store.Replace("/config/manager", configfile)
        finally:
            if store:
                store.Quit()

