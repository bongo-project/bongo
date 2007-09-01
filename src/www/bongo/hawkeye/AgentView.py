import bongo.commonweb
import bongo.commonweb.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler

doneop = 0

class AgentHandler(HawkeyeHandler):

    def index_GET(self, req, rp):
        global doneop
        
        config = self._getAgents(req)
        
        # change things which eval. to False to empty elements
        if config != None and config.has_key("agents"):
            for agent in config["agents"]:
               # We can change this if needed?
               agent["url"] = agent["name"]
               agent["rname"] = self._humanReadableName(agent["name"])
               agent["img"] = "../img/agent-" + agent["name"] + ".png"
               if not agent["enabled"]:
                   agent["enabled"] = None
            self.SetVariable("agentlist", config["agents"])
            self.SetVariable("success", 1)
        elif config != None:
            self.SetVariable("success", 0)
            self.SetVariable("error", "We managed to read the configuration from store, but your 'manager' config file doesn't contain any agents list.")
        else:
            self.SetVariable("success", 0)
            #self.SetVariable("error", "Unable to read agent list from store.")
            
        if doneop:
            self.SetVariable("opsuccess", 1)
            doneop = 0
        else:
            self.SetVariable("opsuccess", 0)
        
        self.SetVariable("agntab", "selecteditem")
        return self.SendTemplate(req, rp, "index.tpl")
        
    def index_POST(self, req, rp):    
        global doneop
        
        if not req.form:
            return bongo.commonweb.HTTP_UNAUTHORIZED
        
        if not req.form.has_key("command"):
            return bongo.commonweb.HTTP_UNAUTHORIZED
        
        # Operation completed OK.
        doneop = 1
        return self.index_GET(req, rp)
        
    def _humanReadableName(self, agentname)
        # TODO: Translation, once we have a system in place.
        if agentname == "bongoqueue":
            return "Queue"
        elif agentname == "bongosmtp":
            return "SMTP"
        elif agentname == "bongoantispam":
            return "Antispam"
        elif agentname == "bongoavirus":
            return "Antivirus"
        elif agentname == "bongocollector":
            return "Calendar collector"
        elif agentname == "bongomailprox":
            return "Mail proxy"
        elif agentname == "bongopluspack":
            return "Plus pack"
        elif agentname == "bongorules":
            return "Rules"
        elif agentname == "bongoimap":
            return "IMAP"
        elif agentname == "bongopop3":
            return "POP3"
        elif agentname == "bongocalcmd":
            return "iCal"
        else:
            return "Unknown agent"
    
    def _getAgents(self, req):
        config = {}
        store = None
        decoder = simplejson.JSONDecoder()
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            configfile = store.Read("/config/manager")
            config = decoder.decode(configfile)
            store.Quit()
        except ValueError:
            self.SetVariable("error", "Failed to parse JSON.")
            if store: 
                store.Quit()
            return None
        except Exception, e:
            self.SetVariable("error", "Unable to read agent list form store: %s" % e)
            if store: 
                store.Quit()
            return None
        
        return config
