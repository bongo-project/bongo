import bongo.commonweb
import bongo.commonweb.BongoUtil
from HawkeyeHandler import HawkeyeHandler

# Status of operation output (if any).
doneop = 0

# Agent definitions
agentdefs = { 'bongoantispam' : {
         'filename' : '/config/antispam',
         'data' : {
            'timeout' : { 'label' : 'Timeout', 'type' : 'int', 'suffix' : 'milliseconds' },
            'hosts' : { 'label' : 'Hosts', 'type' : 'array,string' }
        }
    },
    'bongoqueue' : {
        'filename' : '/config/queue',
        'data' : {
            'postmaster': { 'label' : 'Postmaster', 'type' : 'string' },
            'officialname' : { 'label' : 'Official name', 'type' : 'string' },
            'hosteddomains' : { 'label' : 'Hosted domains', 'type' : 'array,string' },
            'quotamessage' : { 'label' : 'Quota message', 'type' : 'string' }
        }
    },
    'bongoavirus' : {
        'filename' : '/config/antivirus',
        'data' : {
            'host' : { 'label':'Host', 'type' : 'string' },
            'port' : { 'label' : 'Port', 'type' : 'int' }
        }
    }
}


class AgentHandler(HawkeyeHandler):    
    
    def index_GET(self, req, rp):
        '''
        Handler for main agent list view.
        '''
        global doneop
        
        config = self.GetStoreData(req, "/config/manager")
        
        # change things which eval. to False to empty elements
        if config != None and config.has_key("agents"):
            for agent in config["agents"]:
               # We can change this if needed?
               agent["url"] = "agents/" + agent["name"]
               agent["rname"] = self._humanReadableName(agent["name"])
               agent["img"] = "../img/agent-" + agent["name"] + ".png"
               if not agent["enabled"]:
                   agent["enabled"] = None
            self.SetVariable("agentlist", config["agents"])
            self.SetVariable("success", 1)
            self.SetVariable("error", None)
        elif config != None:
            self.SetVariable("success", 0)
            self.SetVariable("error", "We managed to read the configuration from store, but you don't seem to have any agents setup.")
        else:
            self.SetVariable("success", 0)
            # Error is set for us via self.GetStoreData()
            
        if doneop:
            self.SetVariable("opsuccess", 1)
            doneop = 0
        else:
            self.SetVariable("opsuccess", 0)
        
        self.SetVariable("agntab", "selecteditem")
        return self.SendTemplate(req, rp, "index.tpl", title="Agents")
    
    ###############        AGENTS          ###############
    
    # Antispam
    def bongoantispam_GET(self, req, rp):
        return self.showConfig('bongoantispam', req, rp)
        
    def bongoantispam_POST(self, req, rp):
        return self.saveConfig('bongoantispam', req, rp)
    
    # Antivirus
    def bongoavirus_GET(self, req, rp):
        return self.showConfig('bongoavirus', req, rp)
    
    def bongoavirus_POST(self, req, rp):
        return self.saveConfig('bongoavirus', req, rp)
    
    # Queue
    def bongoqueue_GET(self, req, rp):
        return self.showConfig('bongoqueue', req, rp)
        
    def bongoqueue_POST(self, req, rp):
        return self.saveConfig('bongoqueue', req, rp)
    
    ############### CONFIGURATION HELPERS  ################
    
    def saveConfig(self, agentname, req, rp):
        global doneop, agentdefs
        
        self.SetStoreData(req, agentdefs[agentname]['filename'], self.setAgent(self.GetStoreData(req, agentdefs[agentname]['filename']), req.form, agentdefs[agentname]['data']))
        
        doneop = 1
        return self.showConfig(agentname, req, rp)
    
    def setAgent(self, config, rf, agent):        
        for key in rf:
            value = rf[key].value
            print "Has key: %s (value: %s)" % (key, value)
            
            # Mm, value-type formatting.
            # Note: keys with type 'string' remain unchanged.
            if agent[key]['type'] == 'int':
                value = int(value)
            elif agent[key]['type'] == 'bool':
                value = bool(value)
            elif agent[key]['type'].startswith('array,'):
                # Split value type and determine what kind of array.
                arraytype = agent[key]['type'].split(",")[1]
                
                # Now, split input data into an array, and change valuetypes as required.
                # Input data for this key should be a comma-seperated string of values.
                arraydata = value.split(",")
                for dobj in arraydata:
                    if arraytype == "int":
                        dobj = int(dobj)
                    elif arraytype == "bool":
                        dobj = bool(dobj)
                        
                    # Otherwise, type was string (default).
                    
                # Set the data back.
                value = arraydata    
            
            # Set new value.
            config[key] = value
            
        return config
   
    def showConfig(self, agentname, req, rp):
        global agentdefs
        return self.doAgent(agentname, self.GetStoreData(req, agentdefs[agentname]['filename']), agentdefs[agentname]['data'], req, rp)
    
    def doAgent(self, agentname, config, d, req, rp):
        '''
        Displays an agent configuration page, using information
        provided via method arguments.
        '''
        global doneop
        rkeys = []
        
        if config != None and config.has_key("version"):
            for key in config:
                # Disallow other keys that weren't passed along (eg version, etc)
                if d.has_key(key):
                    nkey = {}
                    nkey["id"] = key
                    nkey["value"] = config[key]
                    nkey["name"] = d[key]['label'] + ":"
                    if d[key].has_key('suffix'):
                        nkey["suffix"] = d[key]['suffix']
                    
                    if d[key]['type'] == "int":
                        nkey["numericentry"] = True
                    elif d[key]['type'] == "bool":
                        nkey["boolentry"] = True
                    elif d[key]['type'] == "string":
                        nkey["textentry"] = True
                    elif d[key]['type'].startswith('array,'):
                        nkey["selectentry"] = True
                        # TODO: Fix this so we can have plain lists, instead of arrays
                        nkey["jsadd"] = "javascript:addToList('" + key + "');"
                        nkey["jsrm"] = "javascript:removeFromList('" + key + "');"
                        nkey["jsid"] = key + "-removebtn"  
                        nkey["selectorid"] = key + "-box"
                        nkey["revertbox"] = key + "-normal"
                        nkey["revertjs"] = "$('" + nkey["revertbox"] + "').hide(); $('" + nkey["selectorid"] + "').show();"
                        
                        strthingy = ""
                        for value in config[key]:
                            strthingy += value + ","
                        
                        nkey["strvalue"] = strthingy

                    rkeys.append(nkey)
                
            self.SetVariable("settinglist", rkeys)
        elif config != None:
            self.SetVariable("success", 0)
            self.SetVariable("error", "Agent does not have a version key.")
        else:
            self.SetVariable("success", 0)
        
        # Check if we've done stuff (ie saving config)
        if doneop:
            self.SetVariable("opsuccess", 1)
            doneop = 0
        else:
            self.SetVariable("opsuccess", 0)
        
        self.SetVariable("agntab", "selecteditem")
        self.SetVariable("name", agentname)
        hname = self._humanReadableName(agentname)
        self.SetVariable("hname", hname)
        return self.SendTemplate(req, rp, "agentview.tpl", title=hname)
    
    def _humanReadableName(self, agentname):
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
            return "Calendar CMD"
        else:
            return "Unknown agent"
