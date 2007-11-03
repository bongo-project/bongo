import bongo.commonweb
import bongo.commonweb.BongoUtil
from HawkeyeHandler import HawkeyeHandler

# Status of operation output (if any).
doneop = 0

# Agent definitions
agentdefs = {
    'global' : {
        'label' : 'Global',
        'description' : 'This page describes the global configuration options available in your Bongo installation.<br />Options here will be used in most agents where required.',
        'filename' : '/config/global',
        'data' : {
            'hostname' : { 'label' : 'Hostname', 'type' : 'string' },
            'hostaddr' : { 'label' : 'Host address', 'type' : 'string' },
            'postmaster' : { 'label' : 'Postmaster' }
        }
    },
    'bongoantispam' : {
        'label' : 'Antispam',
         'filename' : '/config/antispam',
         'data' : {
            'timeout' : { 'label' : 'Timeout', 'type' : 'int', 'suffix' : 'milliseconds' },
            'hosts' : { 'label' : 'Hosts', 'type' : 'hostlist,783' }
        }
    },
    'bongoqueue' : {
        'label' : 'Queue',
        'filename' : '/config/queue',
        'data' : {
            'postmaster': { 'label' : 'Postmaster', 'type' : 'string' },
            'officialname' : { 'label' : 'Official name', 'type' : 'string' },
            'hosteddomains' : { 'label' : 'Hosted domains', 'type' : 'array,string' },
            'quotamessage' : { 'label' : 'Quota message', 'type' : 'string' }
        }
    },
    'bongoavirus' : {
        'label' : 'Antivirus',
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
        global doneop, agentdefs

        # Loop through agent defs and make a list to display
        agentlist = [ ]
        for agent in agentdefs:
            pa = { }
            pa["url"] = "agents/" + agent
            pa["rname"] = agentdefs[agent]["label"]
            pa["img"] = "../img/agent-" + agent + ".png"
            agentlist.append(pa)
        self.SetVariable("agentlist", agentlist)
        self.SetVariable("success", 1)
        self.SetVariable("error", None)

        # Setup the all clear if we saved some stuff.
        if doneop:
            self.SetVariable("opsuccess", 1)
            doneop = 0
        else:
            self.SetVariable("opsuccess", 0)
        
        # Usual template cruft - setup tab and spit the page out.
        self.SetVariable("agntab", "selecteditem")
        return self.SendTemplate(req, rp, "index.tpl", title="Agents")
        
    ################# Agents and junk ####################
    
    def bongoqueue_GET(self, req, rp):
        return self.showConfig("bongoqueue", req, rp)
    
    def bongoqueue_POST(self, req, rp):
        return self.saveConfig("bongoqueue", req, rp)
        
    def global_GET(self, req, rp):
        return self.showConfig("global", req, rp)
    
    def global_POST(self, req, rp):
        return self.saveConfig("global", req, rp)
        
    def bongoantispam_GET(self, req, rp):
        return self.showConfig("bongoantispam", req, rp)
    
    def bongoantispam_POST(self, req, rp):
        return self.saveConfig("bongoantispam", req, rp)
        
    def bongoavirus_GET(self, req, rp):
        return self.showConfig("bongoavirus", req, rp)
    
    def bongoavirus_POST(self, req, rp):
        return self.saveConfig("bongoavirus", req, rp)
    
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
            elif agent[key]['type'].startswith('array,') or agent[key]['type'].startswith('hostlist,'):
                # Split value type and determine what kind of array/default port
                arraytype = agent[key]['type'].split(',')[1]
                
                # Now, split input data into an array, and change valuetypes as required.
                # Input data for this key should be a comma-seperated string of values.
                arraydata = value.split(",")
                for dobj in arraydata:
                    if arraytype == "int":
                        dobj = int(dobj)
                    elif arraytype == "bool":
                        dobj = bool(dobj)
                    elif agent[key]['type'].startswith('hostlist,'):
                        # It's a hostlist, and arraytype is the default port
                        # Check if value has port and/or weighting
                        defaultweight = 1
                        defultport = arraytype
                        
                        if dobj.count(':') == 1:
                            # Port and hostname
                            dobj = dobj + ":" + defaultweight
                        elif dobj.count(':') == 0:
                            # Only hostname
                            dobj = dobj + ":" + defaultport + ":" + defaultweight
                        
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
        global doneop, agentdefs
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
                    elif d[key]['type'].startswith('array,') or d[key]['type'].startswith('hostlist,'):
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
            if agentdefs[agentname].has_key('description'):
                self.SetVariable("description", agentdefs[agentname]["description"])
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
        
        global agentdefs
        self.SetVariable("agntab", "selecteditem")
        self.SetVariable("name", agentname)
        self.SetVariable("hname", agentdefs[agentname]["label"])
        return self.SendTemplate(req, rp, "agentview.tpl", title=agentdefs[agentname]["label"])
