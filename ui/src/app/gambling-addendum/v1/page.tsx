import type {Metadata} from 'next'
import {LegalPage, LegalSection} from '@/components/legal-page'
import i18n from '@/lib/i18n-static'

// Keep in sync with wallet.CurrentGamblingAddendumVersion in the Go API
// (api/internal/domain/wallet/user.go). The gate decision is server-side; this is
// the version the user is reading.
export const metadata: Metadata = {
    title: i18n.t('legal.gambling.v1.metaTitle'),
    description: i18n.t('legal.gambling.v1.metaDescription'),
}

export default function GamblingAddendumPage() {
    return (
        <LegalPage
            title={i18n.t('legal.gambling.v1.title')}
            updatedAt={i18n.t('legal.gambling.v1.updatedAt')}
        >
            <p className="text-xs text-muted-foreground">
                {i18n.t('legal.version')} {i18n.t('legal.gambling.v1.version')}
            </p>

            <p>
                {i18n.t('legal.gambling.v1.intro1a')}
                <strong>{i18n.t('legal.gambling.v1.intro1Bold')}</strong>
                {i18n.t('legal.gambling.v1.intro1b')}
                <a href="/terms-addendum" className="underline underline-offset-4">
                    {i18n.t('legal.gambling.v1.addendumLink')}
                </a>
                {i18n.t('legal.gambling.v1.intro1c')}
                <strong>{i18n.t('legal.gambling.v1.intro1Bold2')}</strong>
                {i18n.t('legal.gambling.v1.intro1d')}
            </p>

            <LegalSection heading={i18n.t('legal.gambling.v1.s1.heading')}>
                <p>
                    {i18n.t('legal.gambling.v1.s1.p1a')}
                    <strong>{i18n.t('legal.gambling.v1.s1.p1Bold1')}</strong>
                    {i18n.t('legal.gambling.v1.s1.p1b')}
                    <strong>{i18n.t('legal.gambling.v1.s1.p1Bold2')}</strong>
                    {i18n.t('legal.gambling.v1.s1.p1c')}
                </p>
                <p>
                    {i18n.t('legal.gambling.v1.s1.p2')}
                    <strong>{i18n.t('legal.gambling.v1.s1.p2Bold')}</strong>
                    {i18n.t('legal.gambling.v1.s1.p2b')}
                </p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.gambling.v1.s2.heading')}>
                <p>
                    {i18n.t('legal.gambling.v1.s2.p1a')}
                    <strong>{i18n.t('legal.gambling.v1.s2.p1Bold')}</strong>
                    {i18n.t('legal.gambling.v1.s2.p1b')}
                </p>
                <p>
                    {i18n.t('legal.gambling.v1.s2.p2a')}
                    <strong>{i18n.t('legal.gambling.v1.s2.p2Bold')}</strong>
                    {i18n.t('legal.gambling.v1.s2.p2b')}
                    <strong>{i18n.t('legal.gambling.v1.s2.p2Bold2')}</strong>
                    {i18n.t('legal.gambling.v1.s2.p2c')}
                </p>
                <p className="font-medium text-foreground">
                    {i18n.t('legal.gambling.v1.s2.p3')}
                </p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.gambling.v1.s3.heading')}>
                <p>
                    {i18n.t('legal.gambling.v1.s3.p1a')}
                    <strong>{i18n.t('legal.gambling.v1.s3.p1Bold')}</strong>
                    {i18n.t('legal.gambling.v1.s3.p1b')}
                </p>
                <p className="font-medium text-foreground">
                    {i18n.t('legal.gambling.v1.s3.p2')}
                </p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.gambling.v1.s4.heading')}>
                <p>{i18n.t('legal.gambling.v1.s4.p1')}</p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.gambling.v1.s5.heading')}>
                <p>{i18n.t('legal.gambling.v1.s5.p1')}</p>
                <p>
                    {i18n.t('legal.gambling.v1.s5.p2a')}
                    <strong>{i18n.t('legal.gambling.v1.s5.p2Bold')}</strong>
                    {i18n.t('legal.gambling.v1.s5.p2b')}
                    <a
                        href="https://www.cvv.org.br"
                        target="_blank"
                        rel="noreferrer"
                        className="underline underline-offset-4"
                    >
                        {i18n.t('legal.gambling.v1.s5.cvvLink')}
                    </a>
                    {i18n.t('legal.gambling.v1.s5.p2c')}
                    <a
                        href="https://jogadoresanonimos.com.br"
                        target="_blank"
                        rel="noreferrer"
                        className="underline underline-offset-4"
                    >
                        {i18n.t('legal.gambling.v1.s5.jaLink')}
                    </a>
                    {i18n.t('legal.gambling.v1.s5.p2d')}
                </p>
            </LegalSection>
        </LegalPage>
    )
}
