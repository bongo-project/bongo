import bongo.dragonfly
import bongo.dragonfly.BongoUtil
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
            for key in config:
                if key == "timeout":
                    if config[key] > 1:
                        self.SetVariable("timeout", config[key])
                    else:
                        self.SetVariable("timeout", "20")
                        nosetcheck = 1
                elif key == "host":
                    hval = config[key].split(':', 3)
                    if len(hval) > 1:
                        self.SetVariable("host", hval[0])
                        self.SetVariable("port", hval[1])
                    else:
                        self.SetVariable("host", "not set")
                        self.SetVariable("port", "783")
                        
                        if nosetcheck:
                            self.SetVariable("error", "Antispam is not currently set up correctly. Please enter the correct values below, and click save to rectify this.")
        else:
            self.SetVariable("success", 0)
            self.SetVariable("error", "Unable to read configuration information from the Bongo store. Are you logged in as a user with administrative permissions?")

        self.SetVariable("breadcrumb", "Agents &#187 Antispam")
        self.SetVariable("title", "Antispam")
        self.SetVariable("agntab", "selecteditem")

        return self.SendTemplate(req, rp, "index.tpl")

    def index_POST(self, req, rp):
        global doneop
        
        config = self._getAntispam(req)
        if config.has_key("enabled"):
            for key in req.form:
                print "got key=" + str(key)
                print "val=" + str(req.form[key].value)
                if key == "timeout":
                    config["timeout"] = int(req.form[key].value);
                elif key == "host":
                    config["host"] = req.form[key].value;
                elif key == "port":
                    config["host"] += ":" + req.form[key].value;
            
            # Set weight (always 1 since we can only handle 1 host)
            config["host"] += ":1"
        else:
            self.SetVariable("error", "Unable to read config")

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

