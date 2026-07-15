import type {Metadata} from 'next'
import {LegalPage, LegalSection} from '@/components/legal-page'
import i18n from '@/lib/i18n-static'

// Keep in sync with wallet.CurrentTermsAddendumVersion in the Go API
// (api/internal/domain/wallet/user.go). The gate decision is server-side; this
// is the version the user is reading.
export const metadata: Metadata = {
    title: i18n.t('legal.terms.v1.metaTitle'),
    description: i18n.t('legal.terms.v1.metaDescription'),
}

export default function TermsAddendumPage() {
    return (
        <LegalPage
            title={i18n.t('legal.terms.v1.title')}
            updatedAt={i18n.t('legal.terms.v1.updatedAt')}
        >
            <p className="text-xs text-muted-foreground">
                {i18n.t('legal.version')} {i18n.t('legal.terms.v1.version')}
            </p>

            <p>
                {i18n.t('legal.terms.v1.intro1')}{' '}
                <a
                    href="https://accounts.aoctech.app/terms"
                    className="underline underline-offset-4"
                    target="_blank"
                    rel="noreferrer"
                >
                    {i18n.t('legal.terms.v1.termsOfUse')}
                </a>{' '}
                {i18n.t('legal.terms.v1.intro2')}{' '}
                <a
                    href="https://accounts.aoctech.app/privacy"
                    className="underline underline-offset-4"
                    target="_blank"
                    rel="noreferrer"
                >
                    {i18n.t('legal.terms.v1.privacy')}
                </a>{' '}
                {i18n.t('legal.terms.v1.intro3')}
            </p>

            <LegalSection heading={i18n.t('legal.terms.v1.s1.heading')}>
                <p>
                    {i18n.t('legal.terms.v1.s1.p1a')}
                    <strong>{i18n.t('legal.terms.v1.s1.p1Bold1')}</strong>
                    {i18n.t('legal.terms.v1.s1.p1b')}
                    <strong>{i18n.t('legal.terms.v1.s1.p1Bold2')}</strong>
                    {i18n.t('legal.terms.v1.s1.p1c')}
                </p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.terms.v1.s2.heading')}>
                <p>{i18n.t('legal.terms.v1.s2.p1')}</p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.terms.v1.s3.heading')}>
                <p>{i18n.t('legal.terms.v1.s3.p1')}</p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.terms.v1.s4.heading')}>
                <p>{i18n.t('legal.terms.v1.s4.p1')}</p>
                <p>{i18n.t('legal.terms.v1.s4.p2')}</p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.terms.v1.s5.heading')}>
                <p>{i18n.t('legal.terms.v1.s5.p1')}</p>
                <p className="font-medium text-foreground">
                    {i18n.t('legal.terms.v1.s5.p2')}
                </p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.terms.v1.s6.heading')}>
                <p>{i18n.t('legal.terms.v1.s6.p1')}</p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.terms.v1.s7.heading')}>
                <p>{i18n.t('legal.terms.v1.s7.p1')}</p>
            </LegalSection>

            <LegalSection heading={i18n.t('legal.terms.v1.s8.heading')}>
                <p>
                    {i18n.t('legal.terms.v1.s8.p1a')}
                    <a href="mailto:dpo@aoctech.app" className="underline underline-offset-4">
                        {i18n.t('legal.terms.v1.s8.dpoLink')}
                    </a>
                    {i18n.t('legal.terms.v1.s8.p1b')}
                </p>
            </LegalSection>
        </LegalPage>
    )
}
