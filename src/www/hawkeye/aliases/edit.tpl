%(include|header.tpl)s

<div class="breadcrumb"><p><a href="%(url|/)ssystem">System</a> &#187 <a href="%(url|/)saliases">Aliases</a>&#187 <a href="#">Editing <span tal:content="name"></span></a></p> </div>

<h1>Editing <span tal:content="name"></span></h1>

<p>

<form method="post">
<table cellpadding="1">

<tr>
    <td style="padding-right: 16px;">Domain name:</td><td><span tal:content="name"></span><input type="text" id="name" name="name" tal:condition="newmode" /></td>
</tr>
<tr>
    <td style="padding-right: 16px;">Domain-to-domain aliasing</td>
    <td>
        <input type="checkbox" id="domainwide" name="domainwide" tal:attributes="checked domainwide" /><label for="domainwide">Enable domain-wide aliasing</label>
        <br />Forward mail to: <input type="text" id="domainalias" name="domainalias" tal:attributes="value domainalias" />
    </td>
</tr>
<tr> 
    <td style="padding-right: 16px;">User-based aliasing</td>
    <td>
        <div style="width: 325px; visibility: none">
            <div style="width: 25px; float: right;">
                <a href="javascript:addToList('useralias', 'Please enter the user alias in the following format:\n\n<username>=<newdomain>');" style="float: right;"><img src="/img/list-add.png" width="16" height="16" border="0" alt="Add" /></a><br />
                <a href="javascript:removeFromList('useralias');" id="useralias-removebtn" style="float: right;"><img src="/img/list-remove.png" width="16" height="16" border="0" alt="Remove" /></a>
            </div>
        
            <select id="useralias-box" style="width: 300px; border: 1px solid #888a85; background-color: #fff; display: none" size="5">
                <option tal:repeat="op useraliases" tal:content="op"></option>
            </select>
        </div>
        <div id="useralias-normal">
            	<!-- For non-JS browsers -->
            	<input type="text" name="useralias" id="useralias" tal:attributes="value useraliasestxt" />
            	<p>Please enter values into the above textbox, seperated by a comma.</p>
        </div>
        
        <script type="text/javascript">
            $('useralias-normal').hide(); $('useralias-box').show();
        </script>
    </td>
</tr>
</table>
<br />
<br />
<span class="button"><button type="reset" value="Reset">Reset</button></span> <span class="button"><button type="submit" value="Save">Save</button></span>
</form>


</p>

%(include|footer.tpl)s
