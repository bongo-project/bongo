%(include|header.tpl)s

<div class="breadcrumb"><p><a href="%(url|/)ssystem">System</a> &#187 <a href="%(url|/)saliases">Aliases</a>&#187 <a href="#">Editing <span tal:content="name"></span></a></p> </div>

<h1>Editing <span tal:content="name"></span></h1>

<p>

<form method="post">
<table cellpadding="1">

<tr>
    <td style="padding-right: 16px;">Domain name:</td><td><span tal:content="name"></span></td>
</tr>
<tr>
    <td style="padding-right: 16px;" colspan="2"><input type="checkbox" id="domainwide" /><label for="domainwide">Enable domain-wide aliasing</label></td>
</tr>
<tr> 
    <td style="padding-right: 16px;">User-based aliasing</td>
    <td>
        <div style="width: 325px; visibility: none">
            <div style="width: 25px; float: right;">
                <a href="javascript:addToList('useralias');" style="float: right;"><img src="/img/list-add.png" width="16" height="16" border="0" alt="Add" /></a><br />
                <a href="javascript:removeFromList('useralias');" id="useralias-removebtn" style="float: right;"><img src="/img/list-remove.png" width="16" height="16" border="0" alt="Remove" /></a>
            </div>
        
            <select id="useralias-box" style="width: 300px; border: 1px solid #888a85; background-color: #fff;" size="5">
                
            </select>
        </div>
        <div id="useralias-normal">
            	<!-- For non-JS browsers -->
            	<input type="text" name="useralias" id="useralias" />
            	<p>Please enter values into the above textbox, seperated by a comma.</p>
        </div>
        
        <script type="text/javascript">
            $('useralias-box').hide(); $('useralias-box').show();
        </script>
    </td>
</tr>
</table>
</form>

</p>

%(include|footer.tpl)s
