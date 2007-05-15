import bongo.dragonfly
import bongo.dragonfly.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler

doneop = 0

class AgentHandler(HawkeyeHandler):

    def index_GET(self, req, rp):
        global doneop
        
        if doneop:
            self.SetVariable("opsuccess", 1)
            doneop = 0
        else:
            self.SetVariable("opsuccess", 0)
        
        self.SetVariable("breadcrumb", "Agents")
        self.SetVariable("agntab", "selecteditem")
        self.SetVariable("title", "Agents")
        return self.SendTemplate(req, rp, "index.tpl")
        
    def index_POST(self, req, rp):    
        global doneop
        
        if not req.form:
            return bongo.dragonfly.HTTP_UNAUTHORIZED
        
        if not req.form.has_key("command"):
            return bongo.dragonfly.HTTP_UNAUTHORIZED
        
        # Operation completed OK.
        doneop = 1
        return self.index_GET(req, rp)
    
    def _getAntispam(self, req):
        config = {}
        store = None
        decoder = simplejson.JSONDecoder()
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            configfile = store.Read("/config/antispam")
            config = decoder.decode(configfile)
            store.Quit()
        except:
            if store: 
                store.Quit()
            return None
        
        return config
    
    def _setAntispam(self, req, obj):
        encoder = simplejson.JSONEncoder()
        store = None
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            configfile = encoder.encode(obj)
            store.Replace("/config/antispam", configfile)
        finally:
            if store:
                store.Quit()
