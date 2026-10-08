<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:output method="xml" indent="no"/>
  <xsl:template match="/library">
    <titles count="{count(book)}">
      <xsl:for-each select="book">
        <xsl:sort select="@year" data-type="number" order="descending"/>
        <t year="{@year}"><xsl:value-of select="translate(title, 'abcdefghijklmnopqrstuvwxyz', 'ABCDEFGHIJKLMNOPQRSTUVWXYZ')"/></t>
      </xsl:for-each>
    </titles>
  </xsl:template>
</xsl:stylesheet>
