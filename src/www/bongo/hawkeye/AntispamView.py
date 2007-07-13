import bongo.commonweb
import bongo.commonweb.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler

doneop = 0

class AntispamHandler(HawkeyeHandler):
    def NeedsAuth(self, rp):
        return True

    def index_GET(self, req, rp):
        global doneop
        config = self._getAntispam(req)
        
        if doneop:
            self.SetVariable("opsuccess", 1)
            doneop = 0
        else:
            self.SetVariable("opsuccess", 0)
        
        nosetcheck = 0
        
        # Set values to display back
        if config != None and config.has_key("enabled"):
            self.SetVariable("success", 1)
            if len(config) < 3:
                nosetcheck = 1
            else:
                nosetcheck = 0
            
            for key in config:
                if key == "timeout":
                    if int(config[key]) > 1:
                        self.SetVariable("timeout", config[key])
                elif key == "host":
                    hval = config[key].split(':', 3)
                    if len(hval) > 1:
                        self.SetVariable("host", hval[0])
                        self.SetVariable("port", hval[1])
                        
            if nosetcheck == 1:
                self.SetVariable("error", "Antispam is not currently set up. Please enter the correct values below or accept the defaults, and click 'Save'.")
                
                # Defaults (TODO: move back into store)
                self.SetVariable("timeout", 20)
                self.SetVariable("host", "127.0.0.1")
                self.SetVariable("port", 783)
            else:
                self.SetVariable("error", 0)
                self.SetVariable("success", 1)
        else:
            self.SetVariable("success", 0)
            self.SetVariable("error", "Unable to read configuration information from the Bongo store. Are you logged in as a user with administrative permissions?")

        self.SetVariable("agntab", "selecteditem")

        return self.SendTemplate(req, rp, "index.tpl")

    def index_POST(self, req, rp):
        global doneop
        
        config = self._getAntispam(req)
        if config != None:
            for key in req.form:
                if key == "timeout":
                    config["timeout"] = int(req.form[key].value);
                elif key == "host":
                    config["host"] = req.form[key].value;
                elif key == "port":
                    config["host"] += ":" + req.form[key].value;
            
            # Set weight (always 1 since we can only handle 1 host)
            config["host"] += ":1"
        else:
            config = "{ version: 1, enabled: 1, timeout: 20, host: \"127.0.0.1:783:1\" }"

        self._setAntispam(req, config)
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

